package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/domain"
)

func TestMessageWebhookDeliversOTP(t *testing.T) {
	for _, kind := range []string{"otp"} {
		t.Run(kind, func(t *testing.T) {
			const target = "https://receiver.example.com/hooks/PRIVATE_PATH?token=PRIVATE_QUERY"
			config, err := json.Marshal(domain.MessageNotificationConfig{Provider: "webhook", WebhookURL: target})
			if err != nil {
				t.Fatal(err)
			}
			notifier := NewConfiguredMessageNotifier(reportNotificationSettings{value: string(config)})
			calls := 0
			notifier.webhookClient.Transport = reportHTTPTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodPost || request.URL.String() != target || request.Header.Get("Content-Type") != "application/json" {
					t.Fatal("unexpected webhook request")
				}
				var payload map[string]any
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload["type"] != kind {
					t.Fatalf("unexpected event type: %v", payload["type"])
				}
				if kind == "otp" {
					if payload["email"] != "admin@example.test" || payload["code"] != "123456" || payload["expires_in"] != float64(300) || payload["subject"] != "BOS API verification code" || payload["text"] != "Your BOS API verification code is 123456. It expires in 5 minutes." {
						t.Fatal("OTP request fields do not match the delivery contract")
					}
				} else {
					if payload["subject"] != "Usage report" || payload["text"] != "Organization | Usage" || len(payload) != 3 {
						t.Fatal("report request fields do not match the delivery contract")
					}
				}
				return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
			})
			if kind == "otp" {
				err = notifier.SendOTP(context.Background(), "admin@example.test", "123456")
			} else {
				t.Fatal("unsupported notification type")
			}
			if err != nil || calls != 1 {
				t.Fatalf("delivery failed: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestMessageWebhookAcceptsGenericResponsesAndSanitizesFailures(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		failure    error
		wantFail   bool
	}{
		{"plain success", "ok", 200, nil, false},
		{"accepted", `{"accepted":true}`, 202, nil, false},
		{"empty success", "", 204, nil, false},
		{"redirect", "PRIVATE_RESPONSE", 302, nil, true},
		{"rejection", "PRIVATE_RESPONSE", 400, nil, true},
		{"rate limited", "PRIVATE_RESPONSE", 429, nil, true},
		{"receiver failure", "PRIVATE_RESPONSE", 503, nil, true},
		{"transport failure", "", 0, errors.New("PRIVATE_TRANSPORT"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newNotificationHTTPClient()
			calls := 0
			client.Transport = reportHTTPTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if test.failure != nil {
					return nil, test.failure
				}
				return &http.Response{StatusCode: test.status, Header: http.Header{"Location": []string{"https://127.0.0.1/PRIVATE_REDIRECT"}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})
			err := sendMessageWebhook(context.Background(), client, "https://receiver.example.com/PRIVATE_URL?key=PRIVATE_QUERY", messageWebhookPayload{Type: "otp", Code: "PRIVATE_OTP"})
			if calls != 1 || (err != nil) != test.wantFail || err != nil && (strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "receiver.example.com")) {
				t.Fatalf("delivery result: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestMessageWebhookRejectsInvalidStoredURLsBeforeSending(t *testing.T) {
	for _, target := range []string{"", "http://receiver.example.com/PRIVATE", "https://127.0.0.1/PRIVATE", "https://user:PRIVATE@receiver.example.com/"} {
		config, err := json.Marshal(domain.MessageNotificationConfig{Provider: "webhook", WebhookURL: target})
		if err != nil {
			t.Fatal(err)
		}
		notifier := NewConfiguredMessageNotifier(reportNotificationSettings{value: string(config)})
		notifier.webhookClient.Transport = reportHTTPTransport(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid stored URL reached the transport")
			return nil, nil
		})
		if err := notifier.SendOTP(context.Background(), "admin@example.test", "PRIVATE_OTP"); err == nil || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatalf("invalid URL was not safely rejected: %v", err)
		}
	}
}

func TestMessageWebhookHonorsRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := newNotificationHTTPClient()
	client.Transport = reportHTTPTransport(func(request *http.Request) (*http.Response, error) {
		if request.Context().Err() != context.Canceled {
			t.Fatal("request did not inherit cancellation")
		}
		return nil, request.Context().Err()
	})
	if err := sendMessageWebhook(ctx, client, "https://receiver.example.com/hook", messageWebhookPayload{Type: "otp"}); err == nil {
		t.Fatal("cancelled delivery succeeded")
	}
}
