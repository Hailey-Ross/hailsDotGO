package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/token"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
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

// sendFCM delivers one Android push.
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
func (n *pushNotifier) sendFCM(deviceToken, title, body string, data map[string]string, androidChannel string) {
	if n.fcmHTTP == nil {
		return
	}
	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", n.fcmProjectID)
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
	payload, _ := json.Marshal(map[string]any{"message": msg})
	resp, err := n.fcmHTTP.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("push FCM send: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("push FCM: status %d for token %.8s...", resp.StatusCode, deviceToken)
	}
}

func (n *pushNotifier) sendAPNs(deviceToken, title, body string, data map[string]string) {
	if n.apns == nil {
		return
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
		return
	}
	if !res.Sent() {
		log.Printf("push APNs: %s for token %.8s...", res.Reason, deviceToken)
	}
}

// sendPushToUsers looks up all device tokens for the given user IDs and dispatches
// push notifications asynchronously. Failures are logged and never propagate.
// Call as `go h.sendPushToUsers(...)` from within locked code to avoid holding locks.
func (h *Handlers) sendPushToUsers(userIDs []uint, title, body string, data map[string]string) {
	h.sendPushToUsersOnChannel(userIDs, title, body, data, "")
}

// sendPushToUsersOnChannel is sendPushToUsers with an explicit Android
// notification channel. See sendFCM for why "" is the default everywhere else.
// iOS has no equivalent and ignores it.
//
// Delivery is one blocking HTTP round trip per device token, in order. That is
// fine for a raid lobby of four and is the wrong shape for an event with a few
// hundred subscribers; a worker pool belongs here before this gets popular.
func (h *Handlers) sendPushToUsersOnChannel(userIDs []uint, title, body string, data map[string]string, androidChannel string) {
	if h.notifier == nil || len(userIDs) == 0 {
		return
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
		return
	}
	defer rows.Close()

	for rows.Next() {
		var platform, tok string
		if rows.Scan(&platform, &tok) != nil {
			continue
		}
		switch platform {
		case "android":
			h.notifier.sendFCM(tok, title, body, data, androidChannel)
		case "ios":
			h.notifier.sendAPNs(tok, title, body, data)
		}
	}
}
