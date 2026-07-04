package mail

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendDisabledIsNoop(t *testing.T) {
	m := &Mailer{client: &http.Client{Timeout: time.Second}, api: "http://127.0.0.1:1"}
	if err := m.Send("a@b.c", "subj", "<p>x</p>", "x"); err != nil {
		t.Fatalf("disabled Send should return nil, got %v", err)
	}
}

func TestSendPostsToResend(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"test"}`))
	}))
	defer ts.Close()

	m := &Mailer{apiKey: "re_test", from: "hailsDotGO <noreply@hails.cc>", client: ts.Client(), api: ts.URL}
	if err := m.Send("user@example.com", "Hello", "<p>hi</p>", "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotAuth != "Bearer re_test" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotBody["from"] != "hailsDotGO <noreply@hails.cc>" || gotBody["subject"] != "Hello" {
		t.Errorf("payload = %v", gotBody)
	}
	to, _ := gotBody["to"].([]any)
	if len(to) != 1 || to[0] != "user@example.com" {
		t.Errorf("to = %v", gotBody["to"])
	}
}

func TestSendReportsAPIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"invalid key"}`))
	}))
	defer ts.Close()

	m := &Mailer{apiKey: "re_bad", from: "x <a@b.c>", client: ts.Client(), api: ts.URL}
	err := m.Send("user@example.com", "Hello", "h", "t")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected 403 error, got %v", err)
	}
}

func TestPasswordResetEmail(t *testing.T) {
	subject, htmlBody, textBody := PasswordResetEmail("Hails", "https://pogo.hails.live/reset-password?token=abc")
	if subject == "" {
		t.Fatal("empty subject")
	}
	for _, body := range []string{htmlBody, textBody} {
		if !strings.Contains(body, "https://pogo.hails.live/reset-password?token=abc") {
			t.Errorf("body missing link: %s", body)
		}
		if !strings.Contains(body, "Hails") {
			t.Errorf("body missing username")
		}
	}
	sub, esc, _ := PasswordResetEmail(`<b>x</b>`, "https://example.com/?a=1&b=2")
	_ = sub
	if strings.Contains(esc, "<b>x</b>") {
		t.Error("username not HTML-escaped in html body")
	}
}
