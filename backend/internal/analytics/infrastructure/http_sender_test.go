package infrastructure

import (
	"bosapi/backend/internal/analytics/domain"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func minimalBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(domain.Installation(domain.Metadata{Version: "1.2.3", OS: "linux", Arch: "amd64"}, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestHTTPSenderDoesNotFollowRedirectsOrUseBusinessHeaders(t *testing.T) {
	valid := minimalBody(t)
	var destination atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destination.Add(1) }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("unexpected headers")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != string(valid) {
			t.Error(string(body))
		}
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	sender := NewHTTPSender(time.Second)
	defer sender.CloseIdleConnections()
	sender.client.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig
	result, err := sender.Send(context.Background(), server.URL, valid)
	if err != nil || result.Status != 307 || destination.Load() != 0 {
		t.Fatalf("result=%+v err=%v redirected=%d", result, err, destination.Load())
	}
}

func TestHTTPSenderTimeoutAndBoundedResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/blocked" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Retry-After", "300")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, strings.Repeat("sensitive receiver response", 4096))
	}))
	defer server.Close()
	sender := NewHTTPSender(time.Second)
	defer sender.CloseIdleConnections()
	sender.client.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig
	result, err := sender.Send(context.Background(), server.URL, minimalBody(t))
	if err != nil || result.Status != 429 || result.RetryAfter != 5*time.Minute {
		t.Fatal(result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := sender.Send(ctx, server.URL+"/blocked", minimalBody(t)); err == nil {
		t.Fatal("timeout ignored")
	}
	now := time.Now().Truncate(time.Second)
	if retryAfter(now.Add(time.Hour).UTC().Format(http.TimeFormat), now) != time.Hour || retryAfter("invalid", now) != 0 {
		t.Fatal("Retry-After parser")
	}
}

func TestHTTPSenderRejectsUntrustedBodiesBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	sender := NewHTTPSender(time.Second)
	defer sender.CloseIdleConnections()
	for _, body := range []string{`{"prompt":"private content"}`, `{"api_key":"full-credential"}`, `{"schema_version":1}`, `{}`} {
		if _, err := sender.Send(context.Background(), server.URL, []byte(body)); err == nil {
			t.Fatal("untrusted body accepted")
		}
	}
	for _, endpoint := range []string{"http://receiver.invalid", "https://user:full-credential@receiver.invalid", "https://receiver.invalid?token=full-credential"} {
		if _, err := sender.Send(context.Background(), endpoint, minimalBody(t)); err == nil {
			t.Fatal("credential-bearing receiver accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unexpected network")
	}
}
