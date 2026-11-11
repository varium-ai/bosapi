package subscription

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type catalogRoundTripper func(*http.Request) (*http.Response, error)

func (f catalogRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOpenAIModelsUsesCodexCatalogVersionAndAccount(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: catalogRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://chatgpt.com/backend-api/codex/models?client_version=0.166.0" {
			t.Errorf("unexpected catalog URL: %s", req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer access" || req.Header.Get("ChatGPT-Account-ID") != "account" {
			t.Error("catalog request is missing account credentials")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"models":[]}`)), Header: make(http.Header)}, nil
	})}}
	if _, err := client.Models(context.Background(), "openai", Credentials{AccessToken: "access", AccountID: "account"}); err != nil {
		t.Fatal(err)
	}
}
