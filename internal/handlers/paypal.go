package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

var ppToken struct {
	value     string
	expiresAt time.Time
	mu        sync.Mutex
}

func paypalBase() string {
	if os.Getenv("PAYPAL_MODE") == "live" {
		return "https://api-m.paypal.com"
	}
	return "https://api-m.sandbox.paypal.com"
}

func paypalEnabled() bool {
	return os.Getenv("PAYPAL_CLIENT_ID") != "" && os.Getenv("PAYPAL_CLIENT_SECRET") != ""
}

func paypalAuth() (string, error) {
	ppToken.mu.Lock()
	defer ppToken.mu.Unlock()
	if ppToken.value != "" && time.Now().Before(ppToken.expiresAt) {
		return ppToken.value, nil
	}
	clientID := os.Getenv("PAYPAL_CLIENT_ID")
	secret := os.Getenv("PAYPAL_CLIENT_SECRET")
	if clientID == "" || secret == "" {
		return "", fmt.Errorf("PayPal credentials not configured")
	}
	req, err := http.NewRequest("POST", paypalBase()+"/v1/oauth2/token",
		bytes.NewBufferString("grant_type=client_credentials"))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(clientID+":"+secret)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	ppToken.value = tok.AccessToken
	ppToken.expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn-60) * time.Second)
	return tok.AccessToken, nil
}

type ppOrderResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Links  []struct {
		Href string `json:"href"`
		Rel  string `json:"rel"`
	} `json:"links"`
}

func createPayPalOrder(amountCents int, itemName, returnURL, cancelURL string) (*ppOrderResult, error) {
	token, err := paypalAuth()
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"intent": "CAPTURE",
		"purchase_units": []map[string]any{{
			"amount": map[string]any{
				"currency_code": "USD",
				"value":         fmt.Sprintf("%.2f", float64(amountCents)/100.0),
			},
			"description": itemName,
		}},
		"application_context": map[string]any{
			"return_url": returnURL,
			"cancel_url": cancelURL,
		},
	})
	req, err := http.NewRequest("POST", paypalBase()+"/v2/checkout/orders", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result ppOrderResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func verifyPayPalWebhook(headers http.Header, body []byte) bool {
	webhookID := os.Getenv("PAYPAL_WEBHOOK_ID")
	if webhookID == "" {
		return false
	}
	token, err := paypalAuth()
	if err != nil {
		return false
	}
	payload, _ := json.Marshal(map[string]any{
		"auth_algo":         headers.Get("Paypal-Auth-Algo"),
		"cert_url":          headers.Get("Paypal-Cert-Url"),
		"transmission_id":   headers.Get("Paypal-Transmission-Id"),
		"transmission_sig":  headers.Get("Paypal-Transmission-Sig"),
		"transmission_time": headers.Get("Paypal-Transmission-Time"),
		"webhook_id":        webhookID,
		"webhook_event":     json.RawMessage(body),
	})
	req, err := http.NewRequest("POST", paypalBase()+"/v1/notifications/verify-webhook-signature", bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var result struct {
		VerificationStatus string `json:"verification_status"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.VerificationStatus == "SUCCESS"
}

func capturePayPalOrder(orderID string) (*ppOrderResult, error) {
	token, err := paypalAuth()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", paypalBase()+"/v2/checkout/orders/"+orderID+"/capture",
		bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var result ppOrderResult
	json.Unmarshal(b, &result)
	if result.Status != "COMPLETED" {
		return nil, fmt.Errorf("capture status: %s", result.Status)
	}
	return &result, nil
}
