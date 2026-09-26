package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// decodeFCM unwraps the "message" object every FCM v1 request is wrapped in.
func decodeFCM(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Message map[string]any `json:"message"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if envelope.Message == nil {
		t.Fatal("payload has no message object")
	}
	return envelope.Message
}

// TestFCMPayloadCarriesChannel is the only local guard on the one field that decides where a
// backgrounded notification lands.
//
// onMessageReceived runs in the foreground only. Everywhere else Android builds the notification
// itself from the manifest default, which the app pins to the raids channel, so a staff alert
// with no channel of its own buzzes at raid priority under a "Raids" heading. That is invisible
// to every test that watches the app with the screen on.
func TestFCMPayloadCarriesChannel(t *testing.T) {
	payload, err := fcmPayload("tok", "New costume found", "Friede's Goggles is ready to name",
		map[string]string{"type": "costume_review"}, pushChannelAdmin)
	if err != nil {
		t.Fatalf("fcmPayload: %v", err)
	}

	msg := decodeFCM(t, payload)
	android, ok := msg["android"].(map[string]any)
	if !ok {
		t.Fatalf("no android block in %s", payload)
	}
	note, ok := android["notification"].(map[string]any)
	if !ok {
		t.Fatalf("no android.notification in %s", payload)
	}
	if got := note["channel_id"]; got != "admin" {
		t.Errorf("channel_id = %v, want admin", got)
	}

	// The rest of the message is unchanged by the channel, which is the whole reason the app
	// needs nothing but this one key.
	if got := msg["token"]; got != "tok" {
		t.Errorf("token = %v, want tok", got)
	}
	data, ok := msg["data"].(map[string]any)
	if !ok || data["type"] != "costume_review" {
		t.Errorf("data = %v, want type costume_review", msg["data"])
	}
}

// TestFCMPayloadOmitsChannelByDefault holds the other half of the contract. An empty channel must
// send no android block at all rather than an empty one: naming a channel for every push would
// quietly move raid alerts off the high-importance channel that makes them heads-up banners.
func TestFCMPayloadOmitsChannelByDefault(t *testing.T) {
	payload, err := fcmPayload("tok", "Raid Match Found!", "You matched", nil, "")
	if err != nil {
		t.Fatalf("fcmPayload: %v", err)
	}
	if _, present := decodeFCM(t, payload)["android"]; present {
		t.Errorf("android block present with no channel: %s", payload)
	}
}

// stubTransport answers every request with one canned response, so sendFCM can be driven without
// a network or a real project.
type stubTransport struct {
	status int
	body   string
}

func (s stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     http.Header{},
	}, nil
}

const unregisteredBody = `{"error":{"code":404,"message":"NotRegistered","status":"NOT_FOUND",` +
	`"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`

// TestSendFCMReportsOnlyUNREGISTEREDAsDead guards the one push decision that deletes user rows.
//
// The distinction is the whole point. UNREGISTERED means that install is gone and the row is
// junk, so dropping it is housekeeping. Every other failure, including a bare 404 from a
// project path that does not exist, is a problem with US: deleting rows for that would turn a
// fixable configuration mistake into every device on the site having to register again.
func TestSendFCMReportsOnlyUNREGISTEREDAsDead(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantDead bool
	}{
		{"unregistered token", 404, unregisteredBody, true},
		{"project path missing", 404, `{"error":{"code":404,"message":"Requested entity was not found."}}`, false},
		{"credentials rejected", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED"}}`, false},
		{"delivered", 200, `{"name":"projects/hailsdotgo/messages/1"}`, false},
		{"upstream broken", 500, `oh dear`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &pushNotifier{
				fcmProjectID: "hailsdotgo",
				fcmHTTP:      &http.Client{Transport: stubTransport{status: tc.status, body: tc.body}},
			}
			ok, dead := n.sendFCM("tok", "t", "b", nil, pushChannelAdmin)
			if dead != tc.wantDead {
				t.Errorf("dead = %v, want %v", dead, tc.wantDead)
			}
			if ok != (tc.status < 300) {
				t.Errorf("ok = %v for status %d", ok, tc.status)
			}
		})
	}
}

// A notifier with no FCM configured must not report anything dead: nothing was asked, so nothing
// was answered, and an unconfigured dev box would otherwise delete the live site's tokens if it
// ever pointed at the same database.
func TestSendFCMWithoutCredentialsReportsNothingDead(t *testing.T) {
	n := &pushNotifier{}
	ok, dead := n.sendFCM("tok", "t", "b", nil, "")
	if ok || dead {
		t.Errorf("an unconfigured notifier reported ok=%v dead=%v, want neither", ok, dead)
	}
}

// TestPushResultSeparatesTriedFromReached pins the distinction the costume alert record depends
// on. "We ran a fan-out" and "somebody was told" are different facts, and conflating them is how
// a notification gets retired without ever arriving: eleven dead reinstalls counted as eleven
// phones told, and re-raising it took an engineer editing a file on the server.
func TestPushResultSeparatesTriedFromReached(t *testing.T) {
	cases := []struct {
		name string
		res  pushResult
		want bool
	}{
		{"nobody has a device", pushResult{}, false},
		{"every device was a dead reinstall", pushResult{Devices: 11, Dropped: 11}, false},
		{"sent but rejected for some other reason", pushResult{Devices: 2}, false},
		{"one phone got it", pushResult{Devices: 2, Delivered: 1, Dropped: 1}, true},
	}
	for _, tc := range cases {
		if got := tc.res.Reached(); got != tc.want {
			t.Errorf("%s: Reached() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
