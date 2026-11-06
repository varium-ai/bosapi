package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(raw string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw))}
}
func testToken() string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account-1"}}`)) + ".signature"
}

func googleClients(t *testing.T) {
	t.Helper()
	t.Setenv("BOS_GEMINI_OAUTH_CLIENT_ID", "test-gemini-client")
	t.Setenv("BOS_GEMINI_OAUTH_CLIENT_SECRET", "test-gemini-secret")
	t.Setenv("BOS_ANTIGRAVITY_OAUTH_CLIENT_ID", "test-antigravity-client")
	t.Setenv("BOS_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-antigravity-secret")
}

func TestAuthorizationOwnershipStateExpiryAndPKCE(t *testing.T) {
	googleClients(t)
	for _, platform := range []string{"openai", "anthropic", "gemini", "antigravity"} {
		t.Run(platform, func(t *testing.T) {
			var sessions Sessions
			auth, err := sessions.Start("admin:one", platform)
			if err != nil {
				t.Fatal(err)
			}
			uri, _ := url.Parse(auth.AuthURL)
			cfg, _ := authorizationConfiguration(platform)
			if uri.Query().Get("state") != auth.State || uri.Query().Get("code_challenge_method") != "S256" || strings.Contains(auth.AuthURL, "client_secret") {
				t.Fatal("invalid authorization URL")
			}
			if uri.Query().Get("client_id") != cfg.ClientID || uri.Query().Get("redirect_uri") != cfg.RedirectURI || uri.Query().Get("scope") != cfg.Scope {
				t.Fatal("authorization link does not match the selected platform")
			}
			if platform == "gemini" || platform == "antigravity" {
				if uri.Query().Get("access_type") != "offline" || uri.Query().Get("prompt") != "consent" || cfg.ClientID != "test-"+platform+"-client" {
					t.Fatal("Google authorization must use its own offline client")
				}
			}
			for _, invalid := range []struct{ owner, platform, state string }{{"admin:two", platform, auth.State}, {"admin:one", "other", auth.State}, {"admin:one", platform, "wrong"}} {
				if _, _, _, err := sessions.Consume(invalid.owner, invalid.platform, auth.SessionID, "code", invalid.state); err != ErrSession {
					t.Fatal("invalid session accepted")
				}
			}
			code, verifier, _, err := sessions.Consume("admin:one", platform, auth.SessionID, "https://callback.example.com/?code=code&state="+auth.State, "ignored")
			if err != nil || code != "code" {
				t.Fatal(err)
			}
			hash := sha256.Sum256([]byte(verifier))
			if base64.RawURLEncoding.EncodeToString(hash[:]) != uri.Query().Get("code_challenge") {
				t.Fatal("PKCE mismatch")
			}
			if _, _, _, err := sessions.Consume("admin:one", platform, auth.SessionID, "code", auth.State); err != ErrSession {
				t.Fatal("session replay accepted")
			}
			auth, _ = sessions.Start("admin:one", platform)
			item := sessions.items[auth.SessionID]
			item.Expires = time.Now().Add(-time.Minute)
			sessions.items[auth.SessionID] = item
			if _, _, _, err := sessions.Consume("admin:one", platform, auth.SessionID, "code#"+auth.State, auth.State); err != ErrSession {
				t.Fatal("expired session accepted")
			}
		})
	}
}

func TestOAuthExchangeAndRefreshProtocols(t *testing.T) {
	googleClients(t)
	for _, platform := range []string{"openai", "anthropic", "gemini", "antigravity"} {
		t.Run(platform, func(t *testing.T) {
			cfg, err := authorizationConfiguration(platform)
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			client := Client{HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "loadCodeAssist") {
					return response(`{"cloudaicompanionProject":"managed-project"}`), nil
				}
				requests++
				if r.URL.String() != cfg.TokenURL {
					t.Fatalf("unexpected endpoint %s", r.URL)
				}
				fields := map[string]string{}
				if platform == "anthropic" {
					if r.Header.Get("Content-Type") != "application/json" {
						t.Fatal("wrong content type")
					}
					if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					for key := range r.PostForm {
						fields[key] = r.PostForm.Get(key)
					}
				}
				if fields["client_id"] != cfg.ClientID {
					t.Fatal("missing client ID")
				}
				if (platform == "gemini" || platform == "antigravity") && fields["client_secret"] != "test-"+platform+"-secret" {
					t.Fatal("Google client secret was not loaded from its own environment")
				}
				if requests == 1 && (fields["grant_type"] != "authorization_code" || fields["code_verifier"] != "verifier" || fields["redirect_uri"] != cfg.RedirectURI) {
					t.Fatal("invalid exchange")
				}
				if requests == 2 && (fields["grant_type"] != "refresh_token" || fields["refresh_token"] != "refresh-1") {
					t.Fatal("invalid refresh")
				}
				token := testToken()
				refresh := "refresh-1"
				if requests == 2 {
					refresh = "rotated-refresh"
				}
				raw, _ := json.Marshal(map[string]any{"access_token": token, "refresh_token": refresh, "expires_in": 3600})
				return response(string(raw)), nil
			})}}
			credentials, err := client.Exchange(context.Background(), platform, "code", "verifier", "state", "")
			if err != nil || !credentials.Valid(platform) {
				t.Fatalf("exchange: %v", err)
			}
			next, err := client.Refresh(context.Background(), platform, credentials)
			if err != nil || next.RefreshToken != "rotated-refresh" || next.ProjectID != credentials.ProjectID {
				t.Fatalf("refresh: %v", err)
			}
		})
	}
}

func TestOAuthRejectsIncompleteOrUnsafeCredentials(t *testing.T) {
	for _, raw := range []string{`{"access_token":"a","refresh_token":"r","expires_in":0}`, `{"access_token":"a","refresh_token":"r","expires_in":-1}`, `{"access_token":"a","refresh_token":"r","expires_in":9999999999}`, `{"access_token":"a\r\nHeader: bad","refresh_token":"r","expires_in":3600}`, `{"access_token":"a","expires_in":3600}`} {
		client := Client{HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(raw), nil })}}
		if _, err := client.Exchange(context.Background(), "anthropic", "code", "verifier", "state", ""); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestCodeAssistProjectRequiresExplicitProjectForRegisteredAccount(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "cloudresourcemanager.googleapis.com" {
			return response(`{"projects":[]}`), nil
		}
		if !strings.Contains(r.URL.Path, "loadCodeAssist") {
			t.Fatal("must not onboard an existing subscriber")
		}
		return response(`{"paidTier":{"id":"paid"}}`), nil
	})}}
	for _, platform := range []string{"gemini", "antigravity"} {
		if _, err := client.CodeAssistProject(context.Background(), platform, "token", ""); !errors.Is(err, ErrProject) {
			t.Fatal("missing project accepted")
		}
		if project, err := client.CodeAssistProject(context.Background(), platform, "token", "user-project"); err != nil || project != "user-project" {
			t.Fatal(err)
		}
	}
}

func TestGoogleOAuthClientConfiguration(t *testing.T) {
	for _, platform := range []string{"gemini", "antigravity"} {
		prefix := "BOS_" + strings.ToUpper(platform) + "_OAUTH"
		for _, values := range []struct{ id, secret string }{{"", ""}, {"custom-id", ""}, {"", "custom-secret"}, {" ", " "}, {"custom-id", "custom-secret"}, {" custom-id ", " custom-secret "}} {
			t.Run(platform+":"+values.id+":"+values.secret, func(t *testing.T) {
				googleClients(t)
				t.Setenv(prefix+"_CLIENT_ID", values.id)
				t.Setenv(prefix+"_CLIENT_SECRET", values.secret)
				cfg, err := authorizationConfiguration(platform)
				id, secret := strings.TrimSpace(values.id), strings.TrimSpace(values.secret)
				if id == "" || secret == "" {
					if !errors.Is(err, ErrClientConfig) || !strings.Contains(err.Error(), prefix+"_CLIENT_ID") || !strings.Contains(err.Error(), prefix+"_CLIENT_SECRET") {
						t.Fatal("accepted missing OAuth client credentials")
					}
					return
				}
				if err != nil || cfg.ClientID == "" || cfg.ClientSecret == "" {
					t.Fatal("missing OAuth client configuration")
				}
				if cfg.ClientID != id || cfg.ClientSecret != secret {
					t.Fatal("custom client configuration was not used")
				}
			})
		}
	}
}

func TestGoogleIncompleteClientRejectsAuthorizationBeforeNetwork(t *testing.T) {
	for _, test := range []struct{ platform, id, secret string }{
		{"gemini", "", ""},
		{"gemini", "custom-id", ""},
		{"gemini", "", "custom-secret"},
		{"antigravity", "", ""},
		{"antigravity", "custom-id", ""},
		{"antigravity", "", "custom-secret"},
	} {
		t.Run(test.platform+":"+test.id+":"+test.secret, func(t *testing.T) {
			googleClients(t)
			platform := test.platform
			prefix := "BOS_" + strings.ToUpper(platform) + "_OAUTH"
			t.Setenv(prefix+"_CLIENT_ID", test.id)
			t.Setenv(prefix+"_CLIENT_SECRET", test.secret)
			var sessions Sessions
			if auth, err := sessions.Start("admin", platform); !errors.Is(err, ErrClientConfig) || auth.AuthURL != "" {
				t.Fatal("authorization without a configured client was accepted")
			}
			client := Client{HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("missing client configuration must fail before making a network request")
				return nil, ErrToken
			})}}
			if _, err := client.Exchange(context.Background(), platform, "code", "verifier", "state", ""); !errors.Is(err, ErrClientConfig) {
				t.Fatal("exchange without a configured client was accepted")
			}
			if _, err := client.Refresh(context.Background(), platform, Credentials{RefreshToken: "refresh"}); !errors.Is(err, ErrClientConfig) {
				t.Fatal("refresh without a configured client was accepted")
			}
			cfg, err := Configuration(platform)
			if err != nil || cfg.BaseURL != "https://cloudcode-pa.googleapis.com" || cfg.APIFormat != "gemini" || cfg.ClientID != "" || cfg.ClientSecret != "" {
				t.Fatal("endpoint validation must remain independent of client credentials")
			}
		})
	}
}

func TestGoogleConfiguredClientsAuthorizeExchangeAndRefresh(t *testing.T) {
	for _, test := range []struct{ platform, id, secret, redirect string }{
		{"gemini", "test-gemini-client", "test-gemini-secret", "https://codeassist.google.com/authcode"},
		{"antigravity", "test-antigravity-client", "test-antigravity-secret", "http://localhost:8085/callback"},
	} {
		t.Run(test.platform, func(t *testing.T) {
			googleClients(t)
			prefix := "BOS_" + strings.ToUpper(test.platform) + "_OAUTH"
			t.Setenv(prefix+"_CLIENT_ID", test.id)
			t.Setenv(prefix+"_CLIENT_SECRET", test.secret)
			var sessions Sessions
			auth, err := sessions.Start("admin", test.platform)
			if err != nil {
				t.Fatal(err)
			}
			uri, err := url.Parse(auth.AuthURL)
			if err != nil || uri.Host != "accounts.google.com" || uri.Query().Get("client_id") != test.id || uri.Query().Get("redirect_uri") != test.redirect {
				t.Fatal("authorization link did not use the configured client")
			}
			if strings.Contains(auth.AuthURL, "client_secret") || strings.Contains(auth.AuthURL, test.secret) {
				t.Fatal("authorization link must not include the client secret")
			}
			tokenRequests, projectRequests := 0, 0
			client := Client{HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "cloudcode-pa.googleapis.com" && r.URL.Path == "/v1internal:loadCodeAssist" {
					projectRequests++
					return response(`{"cloudaicompanionProject":"managed-project"}`), nil
				}
				if r.URL.String() != "https://oauth2.googleapis.com/token" {
					t.Fatal("unexpected token endpoint")
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.PostForm.Get("client_id") != test.id || r.PostForm.Get("client_secret") != test.secret {
					t.Fatal("token request did not use the configured client pair")
				}
				tokenRequests++
				if tokenRequests == 1 && (r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("code") != "code" || r.PostForm.Get("code_verifier") != "verifier" || r.PostForm.Get("redirect_uri") != test.redirect) {
					t.Fatal("incorrect authorization code exchange")
				}
				if tokenRequests == 2 && (r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "refresh") {
					t.Fatal("incorrect refresh request")
				}
				return response(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`), nil
			})}}
			credentials, err := client.Exchange(context.Background(), test.platform, "code", "verifier", "state", "")
			if err != nil || !credentials.Valid(test.platform) || credentials.ProjectID != "managed-project" {
				t.Fatalf("configured-client exchange failed: %v", err)
			}
			next, err := client.Refresh(context.Background(), test.platform, credentials)
			if err != nil || !next.Valid(test.platform) || next.ProjectID != credentials.ProjectID || tokenRequests != 2 || projectRequests != 1 {
				t.Fatalf("configured-client refresh failed: %v", err)
			}
		})
	}
}
