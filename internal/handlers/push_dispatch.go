package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/token"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// pushChannelAdmin is the Android channel for notifications only staff ever receive. Spell it
// exactly: the app creates the channel under this id ("Admin alerts", IMPORTANCE_DEFAULT) and a
// name that matches nothing falls back to the manifest default, which is the raids channel.
//
// Staff alerts rode that default until 2026-09-21, so a costume waiting to be named buzzed at
// raid priority under a "Raids" heading, and an admin who muted raids stopped hearing about them
// with nothing to show that they had.
const pushChannelAdmin = "admin"

// The report push types, spelled the way the app switches on them. Case sensitive, and the app
// reads nothing off these pushes except the type and the report_id that was always there.
//
// They exist because every one of these went out untyped until 2026-09-21, so the app had nothing
// to match on: a tap opened the dashboard rather than the report it was about. The two reply
// values are not a duplicate. One notification goes to the people following a report and one goes
// to staff watching all of them, and the app picks the foreground channel from the type alone, so
// a single shared value would put one of the two audiences on the wrong channel every time.
const (
	pushTypeReportNewBug     = "report_new_bug"
	pushTypeReportNewPlayer  = "report_new_player"
	pushTypeReportReopened   = "report_reopened"
	pushTypeReportAssigned   = "report_assigned"
	pushTypeReportReplyStaff = "report_reply_staff"
	pushTypeReportReply      = "report_reply"
	pushTypeReportInvited    = "report_invited"
	pushTypeReportActioned   = "report_actioned"
)

type pushNotifier struct {
	fcmProjectID string
	fcmHTTP      *http.Client // oauth2-authorized, auto-refreshes every hour
	apns         *apns2.Client
	bundleID     string
}

func newPushNotifier() *pushNotifier {
	n := &pushNotifier{}

	// FCM (Android) -- requires FCM_PROJECT_ID and FCM_CREDENTIALS_JSON env vars.
	if projectID := os.Getenv("FCM_PROJECT_ID"); projectID != "" {
		if credPath := os.Getenv("FCM_CREDENTIALS_JSON"); credPath != "" {
			jsonBytes, err := os.ReadFile(credPath)
			if err != nil {
				log.Printf("push: FCM credentials file: %v", err)
			} else {
				creds, err := google.CredentialsFromJSON(
					context.Background(), jsonBytes,
					"https://www.googleapis.com/auth/firebase.messaging",
				)
				if err != nil {
					log.Printf("push: FCM credentials parse: %v", err)
				} else {
					n.fcmProjectID = projectID
					n.fcmHTTP = oauth2.NewClient(context.Background(), creds.TokenSource)
				}
			}
		}
	}

	// APNs (iOS) -- requires APNS_KEY_PATH, APNS_KEY_ID, APNS_TEAM_ID, APNS_BUNDLE_ID.
	keyPath := os.Getenv("APNS_KEY_PATH")
	keyID := os.Getenv("APNS_KEY_ID")
	teamID := os.Getenv("APNS_TEAM_ID")
	n.bundleID = os.Getenv("APNS_BUNDLE_ID")
	if keyPath != "" && keyID != "" && teamID != "" && n.bundleID != "" {
		authKey, err := token.AuthKeyFromFile(keyPath)
		if err != nil {
			log.Printf("push: APNs key load: %v", err)
		} else {
			client := apns2.NewTokenClient(&token.Token{
				AuthKey: authKey,
				KeyID:   keyID,
				TeamID:  teamID,
			})
			if os.Getenv("APNS_PRODUCTION") == "true" {
				client = client.Production()
			} else {
				client = client.Development()
			}
			n.apns = client
		}
	}

	return n
}

// fcmPayload builds one Android push, split out from the send so the channel can be asserted in
// a test. Nothing local can otherwise catch a mistake in it, for the reason the last paragraph
// below gives.
//
// androidChannel names the notification channel the app should render it on, and
// "" means "say nothing". Saying nothing is the right default, not an oversight:
// the app's manifest pins FCM's default_notification_channel_id to the raids
// channel, which is what makes a backgrounded raid alert land at high importance
// today. Setting a channel here for every push would quietly move raid alerts
// onto whichever channel was named. Only callers that want a different channel
// pass one.
//
// The block matters solely for the backgrounded and force-stopped cases, where
// the system builds the notification without the app running. A foreground push
// is routed by app code and looks correct even when this is wrong, so testing
// only in the foreground proves nothing about it.
func fcmPayload(deviceToken, title, body string, data map[string]string, androidChannel string) ([]byte, error) {
	msg := map[string]any{
		"token":        deviceToken,
		"notification": map[string]string{"title": title, "body": body},
		"data":         data,
	}
	if androidChannel != "" {
		msg["android"] = map[string]any{
			"notification": map[string]string{"channel_id": androidChannel},
		}
	}
	return json.Marshal(map[string]any{"message": msg})
}

// sendFCM delivers one Android push. See fcmPayload for what goes in it.
//
// It reports what happened: ok when FCM accepted the message, dead when FCM says the token is
// gone for good, which is the caller's cue to delete the row. It deletes nothing itself, because
// this type has no database and should not grow one.
func (n *pushNotifier) sendFCM(deviceToken, title, body string, data map[string]string, androidChannel string) (ok, dead bool) {
	if n.fcmHTTP == nil {
		return false, false
	}
	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", n.fcmProjectID)
	payload, err := fcmPayload(deviceToken, title, body, data, androidChannel)
	if err != nil {
		log.Printf("push FCM payload: %v", err)
		return false, false
	}
	resp, err := n.fcmHTTP.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("push FCM send: %v", err)
		return false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 {
		return true, false
	}

	// The body, not just the status. A 404 is normally UNREGISTERED, meaning that phone
	// reinstalled and this row is dead, but a nonexistent project path 404s too, and those need
	// opposite responses: one is routine and self-healing, the other means nobody has been
	// getting notifications at all. On 2026-09-21 a run of eleven 404s looked exactly like the
	// second and was the first, and the status alone could not settle it.
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	text := strings.Join(strings.Fields(string(detail)), " ")
	log.Printf("push FCM: status %d for token %.8s...: %s", resp.StatusCode, deviceToken, text)

	// Only on the error code, never on the bare 404. Deleting a row because a project id is wrong
	// would turn a fixable configuration mistake into every device having to register again.
	return false, resp.StatusCode == http.StatusNotFound && strings.Contains(text, "UNREGISTERED")
}

// sendAPNs is sendFCM's twin for iOS, and reports the same two things. Apple names a dead token
// two different ways, and both mean the row is dead.
func (n *pushNotifier) sendAPNs(deviceToken, title, body string, data map[string]string) (ok, dead bool) {
	if n.apns == nil {
		return false, false
	}
	payload := map[string]any{
		"aps": map[string]any{
			"alert": map[string]string{"title": title, "body": body},
			"sound": "default",
		},
	}
	for k, v := range data {
		payload[k] = v
	}
	payloadBytes, _ := json.Marshal(payload)
	notif := &apns2.Notification{
		DeviceToken: deviceToken,
		Topic:       n.bundleID,
		Payload:     payloadBytes,
	}
	res, err := n.apns.Push(notif)
	if err != nil {
		log.Printf("push APNs send: %v", err)
		return false, false
	}
	if res.Sent() {
		return true, false
	}
	log.Printf("push APNs: %s for token %.8s...", res.Reason, deviceToken)
	return false, res.Reason == apns2.ReasonUnregistered || res.Reason == apns2.ReasonBadDeviceToken
}

// pushResult is what one fan-out actually achieved, which is not the same question as whether it
// ran. Delivered counts the devices the platform accepted the message for.
//
// It exists because a caller that records "these people have been told" needs to know whether
// anybody was: on 2026-09-21 every token on the site was a dead reinstall, so the costume alert
// was marked as delivered to eleven phones that had not existed for weeks, and the only way to
// send it again was an engineer editing a file on the server.
type pushResult struct {
	Devices   int // rows we tried
	Delivered int // rows the platform accepted
	Dropped   int // rows deleted because the platform said the install is gone
}

// Reached reports whether this notification got to at least one device.
func (r pushResult) Reached() bool { return r.Delivered > 0 }

// sendPushToUsers looks up all device tokens for the given user IDs and dispatches
// push notifications asynchronously. Failures are logged and never propagate.
// Call as `go h.sendPushToUsers(...)` from within locked code to avoid holding locks.
func (h *Handlers) sendPushToUsers(userIDs []uint, title, body string, data map[string]string) pushResult {
	return h.sendPushToUsersOnChannel(userIDs, title, body, data, "")
}

// sendPushToUsersOnChannel is sendPushToUsers with an explicit Android
// notification channel. See sendFCM for why "" is the default everywhere else.
// iOS has no equivalent and ignores it.
//
// Delivery is one blocking HTTP round trip per device token, in order. That is
// fine for a raid lobby of four and is the wrong shape for an event with a few
// hundred subscribers; a worker pool belongs here before this gets popular.
func (h *Handlers) sendPushToUsersOnChannel(userIDs []uint, title, body string, data map[string]string, androidChannel string) pushResult {
	var res pushResult
	if h.notifier == nil || len(userIDs) == 0 {
		return res
	}

	placeholders := make([]string, len(userIDs))
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := h.db.Query(
		`SELECT platform, push_token FROM mobile_device_tokens WHERE user_id IN (`+
			strings.Join(placeholders, ",")+`)`,
		args...,
	)
	if err != nil {
		log.Printf("push: query device tokens: %v", err)
		return res
	}
	defer rows.Close()

	var dead []string
	for rows.Next() {
		var platform, tok string
		if rows.Scan(&platform, &tok) != nil {
			continue
		}
		var ok, gone bool
		switch platform {
		case "android":
			ok, gone = h.notifier.sendFCM(tok, title, body, data, androidChannel)
		case "ios":
			ok, gone = h.notifier.sendAPNs(tok, title, body, data)
		default:
			continue
		}
		res.Devices++
		if ok {
			res.Delivered++
		}
		if gone {
			dead = append(dead, tok)
		}
	}
	// After the loop, not inside it: rows is still open on the same connection, and deleting
	// underneath an unread result set is how you get "commands out of sync" instead of a push.
	h.forgetDeadTokens(dead)
	res.Dropped = len(dead)
	return res
}

// forgetDeadTokens deletes device rows FCM has told us no longer exist.
//
// A reinstall invalidates the old token and the new install has no memory of it, so the app
// cannot clean these up: only the send can, because the send is where the news arrives. Without
// this they accumulate forever, and every push makes a pointless blocking round trip per dead
// row. Eleven of them hid a real question for an afternoon on 2026-09-21, because "every send
// failed" reads like a broken configuration when the denominator is all dead rows.
//
// Safe to lose: if the delete fails, the row simply gets another chance to be reported dead next
// time. It is never called for a delivery failure, only for an explicit UNREGISTERED.
func (h *Handlers) forgetDeadTokens(tokens []string) {
	if len(tokens) == 0 {
		return
	}
	placeholders := make([]string, len(tokens))
	args := make([]any, len(tokens))
	for i, t := range tokens {
		placeholders[i] = "?"
		args[i] = t
	}
	res, err := h.db.Exec(
		`DELETE FROM mobile_device_tokens WHERE push_token IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	if err != nil {
		log.Printf("push: forget %d unregistered token(s): %v", len(tokens), err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("push: forgot %d device token(s) FCM reported as unregistered", n)
	}
}
