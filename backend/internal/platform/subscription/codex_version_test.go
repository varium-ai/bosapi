package subscription

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCodexReleaseRefreshRetainsLastGoodVersion(t *testing.T) {
	s := newCodexVersionState("0.166.0")
	for _, tt := range []struct {
		body   string
		status int
		want   string
		err    bool
	}{
		{`{"tag_name":"rust-v0.167.0"}`, 200, "0.167.0", false},
		{`{"tag_name":"rust-v0.166.0"}`, 200, "0.167.0", false},
		{`{"tag_name":"rust-v0.168.0-alpha.1","prerelease":true}`, 200, "0.167.0", true},
		{`{"tag_name":"rust-v0.168.0","draft":true}`, 200, "0.167.0", true},
		{`{"tag_name":"unrelated-v9.0.0"}`, 200, "0.167.0", true},
		{`{"tag_name":"rust-v9999999999999.0.0"}`, 200, "0.167.0", true},
		{`not json`, 200, "0.167.0", true},
		{`{}`, 429, "0.167.0", true},
		{`{"tag_name":"rust-v0.168.0","body":"` + strings.Repeat("x", 100000) + `"}`, 200, "0.168.0", false},
		{strings.Repeat("x", (2<<20)+1), 200, "0.168.0", true},
	} {
		client := &http.Client{Transport: catalogRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Authorization") != "" || req.Header.Get("ChatGPT-Account-ID") != "" {
				t.Fatal("release check must not send provider credentials")
			}
			return &http.Response{StatusCode: tt.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tt.body))}, nil
		})}
		err := s.refresh(context.Background(), client, codexStableReleaseURL)
		if (err != nil) != tt.err || s.version.Load().(string) != tt.want {
			t.Fatalf("body=%s version=%s err=%v", tt.body, s.version.Load(), err)
		}
	}
}

func TestCodexVersionPinAndHeaderSnapshot(t *testing.T) {
	before := activeCodexVersion.Load()
	t.Cleanup(func() { activeCodexVersion.Store(before) })
	t.Setenv("BOS_CODEX_CLIENT_VERSION", "0.170.2")
	t.Setenv("BOS_CODEX_VERSION_AUTO_UPDATE", "true")
	stop, err := StartCodexVersionSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	headers := make(http.Header)
	version := ApplyCodexClientHeaders(headers)
	if version != "0.170.2" || headers.Get("Version") != version || !strings.HasPrefix(headers.Get("User-Agent"), headers.Get("Originator")+"/"+version+" ") {
		t.Fatalf("inconsistent headers: %v", headers)
	}
	client := Client{HTTP: &http.Client{Transport: catalogRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("client_version") != version || req.Header.Get("Version") != version || req.Header.Get("User-Agent") != headers.Get("User-Agent") {
			t.Fatalf("catalog did not use pinned inference identity: %s %v", req.URL, req.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"models":[]}`))}, nil
	})}}
	if _, err := client.Models(context.Background(), "openai", Credentials{AccessToken: "access", AccountID: "account"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOS_CODEX_CLIENT_VERSION", "0.170.2\r\nInjected: true")
	if _, err := StartCodexVersionSync(context.Background()); err == nil {
		t.Fatal("accepted invalid version")
	}
	t.Setenv("BOS_CODEX_CLIENT_VERSION", "")
	t.Setenv("BOS_CODEX_VERSION_AUTO_UPDATE", "false")
	stopFallback, err := StartCodexVersionSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stopFallback()
	if CodexClientVersion() != DefaultCodexClientVersion {
		t.Fatal("disabled sync did not use fallback")
	}
	t.Setenv("BOS_CODEX_VERSION_AUTO_UPDATE", "invalid")
	if _, err := StartCodexVersionSync(context.Background()); err == nil {
		t.Fatal("accepted invalid sync setting")
	}
}

func TestCodexVersionWorkerStopsWithContext(t *testing.T) {
	before := activeCodexVersion.Load()
	t.Cleanup(func() { activeCodexVersion.Store(before) })
	t.Setenv("BOS_CODEX_CLIENT_VERSION", "")
	t.Setenv("BOS_CODEX_VERSION_AUTO_UPDATE", "true")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before starting so this test performs no release network I/O.
	stop, err := StartCodexVersionSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	stop() // Closing an already-stopped worker is safe.
}
