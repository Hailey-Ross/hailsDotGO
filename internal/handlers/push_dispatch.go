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

func (n *pushNotifier) sendFCM(deviceToken, title, body string, data map[string]string) {
	if n.fcmHTTP == nil {
		return
	}
	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", n.fcmProjectID)
	payload, _ := json.Marshal(map[string]any{
		"message": map[string]any{
			"token":        deviceToken,
			"notification": map[string]string{"title": title, "body": body},
			"data":         data,
		},
	})
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
			h.notifier.sendFCM(tok, title, body, data)
		case "ios":
			h.notifier.sendAPNs(tok, title, body, data)
		}
	}
}
