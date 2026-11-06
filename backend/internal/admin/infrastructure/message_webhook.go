package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"bosapi/backend/internal/admin/domain"
)

type messageWebhookPayload struct {
	Type      string `json:"type"`
	Subject   string `json:"subject"`
	Text      string `json:"text"`
	Email     string `json:"email,omitempty"`
	Code      string `json:"code,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
}

// sendMessageWebhook uses a generic JSON contract and accepts any HTTP 2xx response.
// URLs, OTP codes and receiver responses must never appear in delivery errors.
func sendMessageWebhook(ctx context.Context, client *http.Client, target string, payload messageWebhookPayload) error {
	failure := errors.New("webhook notification delivery failed")
	if err := domain.ValidateMessageWebhookURL(target); err != nil {
		return failure
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return failure
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return failure
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return failure
	}
	return nil
}
