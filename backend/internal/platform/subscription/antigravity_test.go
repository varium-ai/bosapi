package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestAntigravityProjectDiscoveryAndOnboarding(t *testing.T) {
	for _, projectResult := range []string{`"managed-project"`, `{"id":"managed-project"}`} {
		t.Run(projectResult, func(t *testing.T) {
			calls := 0
			client := Client{HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.Host != "cloudcode-pa.googleapis.com" || r.Header.Get("Authorization") != "Bearer account-access" || r.Header.Get("User-Agent") != AntigravityUserAgent {
					t.Fatal("incorrect Antigravity discovery request")
				}
				var body struct {
					Metadata map[string]string `json:"metadata"`
					TierID   string            `json:"tierId"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Metadata["ideType"] != "ANTIGRAVITY" {
					t.Fatal("Gemini metadata was used for Antigravity")
				}
				if calls == 1 {
					if r.URL.Path != "/v1internal:loadCodeAssist" || body.Metadata["ideName"] != "antigravity" || body.Metadata["ideVersion"] == "" {
						t.Fatal("incorrect project discovery")
					}
					return response(`{"allowedTiers":[{"id":"other"},{"id":"available-tier","isDefault":true}]}`), nil
				}
				if r.URL.Path != "/v1internal:onboardUser" || body.TierID != "available-tier" || body.Metadata["platform"] != "PLATFORM_UNSPECIFIED" || body.Metadata["pluginType"] != "GEMINI" {
					t.Fatal("incorrect default tier onboarding")
				}
				return response(`{"done":true,"response":{"cloudaicompanionProject":` + projectResult + `}}`), nil
			})}}
			project, err := client.CodeAssistProject(context.Background(), "antigravity", "account-access", "")
			if err != nil || project != "managed-project" || calls != 2 {
				t.Fatalf("project=%q calls=%d error=%v", project, calls, err)
			}
		})
	}
}

func TestAntigravityModelDiscoveryUsesAccountCatalog(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels" || r.Header.Get("Authorization") != "Bearer account-access" || r.Header.Get("User-Agent") != AntigravityUserAgent {
			t.Fatal("model discovery used the wrong account or service")
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["project"] != "account-project" {
			t.Fatal("model discovery lost the account project")
		}
		return response(`{"models":{"model-z":{"displayName":"Last"},"model-a":{"displayName":"First"}}}`), nil
	})}}
	raw, err := client.Models(context.Background(), "antigravity", Credentials{AccessToken: "account-access", ProjectID: "account-project"})
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &catalog) != nil || len(catalog.Data) != 2 || catalog.Data[0].ID != "model-a" || catalog.Data[1].ID != "model-z" {
		t.Fatalf("invalid catalog: %s", raw)
	}
	client.HTTP.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(`{"models":{}}`), nil })
	if _, err := client.Models(context.Background(), "antigravity", Credentials{}); err == nil {
		t.Fatal("an empty catalog must not pass the connection test")
	}
}

func TestAntigravityRefreshRetainsProjectAndUnrotatedRefreshToken(t *testing.T) {
	googleClients(t)
	client := Client{HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return response(`{"access_token":"renewed","expires_in":3600}`), nil
	})}}
	current := Credentials{AccessToken: "old", RefreshToken: "account-refresh", ProjectID: "account-project", ExpiresAt: time.Now().Add(-time.Hour)}
	next, err := client.Refresh(context.Background(), "antigravity", current)
	if err != nil || next.AccessToken != "renewed" || next.RefreshToken != current.RefreshToken || next.ProjectID != current.ProjectID || !next.Valid("antigravity") {
		t.Fatalf("refresh lost account identity: %v", err)
	}
	for _, field := range []string{"project", "refresh"} {
		invalid := next
		if field == "project" {
			invalid.ProjectID = ""
		} else {
			invalid.RefreshToken = ""
		}
		raw, _ := json.Marshal(invalid)
		if _, err := Decode(string(raw), "antigravity"); err == nil {
			t.Fatalf("accepted missing %s", field)
		}
	}
}
