// Package subscription implements subscription account authorization and credentials.
package subscription

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ErrSession      = errors.New("authorization session is invalid or expired")
	ErrToken        = errors.New("subscription authorization failed")
	ErrClientConfig = errors.New("subscription OAuth client is not configured")
)

type clientConfigError struct{ name, prefix string }

func (e clientConfigError) Error() string {
	return fmt.Sprintf("%s OAuth requires %s_CLIENT_ID and %s_CLIENT_SECRET to be configured", e.name, e.prefix, e.prefix)
}
func (e clientConfigError) Unwrap() error { return ErrClientConfig }

const AntigravityUserAgent = "antigravity/hub/2.23.0"

type Config struct {
	AuthorizeURL, TokenURL, ClientID, ClientSecret, RedirectURI, Scope, BaseURL, APIFormat string
}

// Configuration pins credentials to the provider's official service.
func Configuration(platform string) (Config, error) {
	switch platform {
	case "openai":
		return Config{AuthorizeURL: "https://auth.openai.com/oauth/authorize", TokenURL: "https://auth.openai.com/oauth/token", ClientID: "app_EMoamEEZ73f0CkXaXp7hrann", RedirectURI: "http://localhost:1455/auth/callback", Scope: "openid profile email offline_access", BaseURL: "https://chatgpt.com/backend-api/codex", APIFormat: "openai_responses"}, nil
	case "anthropic":
		return Config{AuthorizeURL: "https://claude.com/cai/oauth/authorize", TokenURL: "https://platform.claude.com/v1/oauth/token", ClientID: "9d1c250a-e61b-44d9-88ed-5944d1962f5e", RedirectURI: "https://platform.claude.com/oauth/code/callback", Scope: "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload", BaseURL: "https://api.anthropic.com", APIFormat: "anthropic_messages"}, nil
	case "gemini":
		return Config{AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", RedirectURI: "https://codeassist.google.com/authcode", Scope: "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile", BaseURL: "https://cloudcode-pa.googleapis.com", APIFormat: "gemini"}, nil
	case "antigravity":
		return Config{AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", RedirectURI: "http://localhost:8085/callback", Scope: "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs", BaseURL: "https://cloudcode-pa.googleapis.com", APIFormat: "gemini"}, nil
	}
	return Config{}, ErrToken
}

// Client credentials are required only for authorization and token rotation;
// endpoint validation and routing do not depend on OAuth client configuration.
func authorizationConfiguration(platform string) (Config, error) {
	cfg, err := Configuration(platform)
	if err != nil {
		return Config{}, err
	}
	if platform == "gemini" || platform == "antigravity" {
		prefix, name := "BOS_GEMINI_OAUTH", "Gemini"
		if platform == "antigravity" {
			prefix, name = "BOS_ANTIGRAVITY_OAUTH", "Antigravity"
		}
		cfg.ClientID = strings.TrimSpace(os.Getenv(prefix + "_CLIENT_ID"))
		cfg.ClientSecret = strings.TrimSpace(os.Getenv(prefix + "_CLIENT_SECRET"))
		if cfg.ClientID == "" || cfg.ClientSecret == "" {
			return Config{}, clientConfigError{name, prefix}
		}
	}
	return cfg, nil
}

// Credentials is serialized only inside the existing encrypted credential column.
type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (c Credentials) Valid(platform string) bool {
	for _, value := range []string{c.AccessToken, c.RefreshToken, c.AccountID, c.ProjectID} {
		if len(value) > 32768 || strings.ContainsAny(value, "\r\n") {
			return false
		}
	}
	return c.AccessToken != "" && c.RefreshToken != "" && !c.ExpiresAt.IsZero() && (platform != "openai" || c.AccountID != "") && (platform != "gemini" && platform != "antigravity" || c.ProjectID != "")
}

func Decode(raw, platform string) (Credentials, error) {
	var c Credentials
	if json.Unmarshal([]byte(raw), &c) != nil || !c.Valid(platform) {
		return Credentials{}, ErrToken
	}
	return c, nil
}

type Client struct{ HTTP *http.Client }

func (c Client) httpClient() *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}
	if c.HTTP != nil {
		*client = *c.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout == 0 {
		client.Timeout = 30 * time.Second
	}
	return client
}

func (c Client) token(ctx context.Context, platform string, values url.Values) (Credentials, error) {
	cfg, err := authorizationConfiguration(platform)
	if err != nil {
		return Credentials{}, err
	}
	values.Set("client_id", cfg.ClientID)
	if cfg.ClientSecret != "" {
		values.Set("client_secret", cfg.ClientSecret)
	}
	var reader io.Reader = strings.NewReader(values.Encode())
	contentType := "application/x-www-form-urlencoded"
	if platform == "anthropic" {
		doc := make(map[string]string, len(values))
		for key := range values {
			doc[key] = values.Get(key)
		}
		raw, _ := json.Marshal(doc)
		reader, contentType = bytes.NewReader(raw), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, reader)
	if err != nil {
		return Credentials{}, ErrToken
	}
	req.Header.Set("Content-Type", contentType)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return Credentials{}, ErrToken
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Credentials{}, ErrToken
	}
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result) != nil || result.AccessToken == "" || result.ExpiresIn <= 0 || result.ExpiresIn > 366*24*3600 {
		return Credentials{}, ErrToken
	}
	return Credentials{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, AccountID: accountID(result.AccessToken), ExpiresAt: time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)}, nil
}

func (c Client) Exchange(ctx context.Context, platform, code, verifier, state, project string) (Credentials, error) {
	cfg, err := Configuration(platform)
	if err != nil {
		return Credentials{}, err
	}
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {cfg.RedirectURI}}
	if platform == "anthropic" {
		values.Set("state", state)
	}
	credentials, err := c.token(ctx, platform, values)
	if err != nil {
		return Credentials{}, err
	}
	if platform == "gemini" || platform == "antigravity" {
		credentials.ProjectID, err = c.CodeAssistProject(ctx, platform, credentials.AccessToken, project)
		if err != nil {
			return Credentials{}, err
		}
	}
	if !credentials.Valid(platform) {
		return Credentials{}, ErrToken
	}
	return credentials, nil
}

func (c Client) Refresh(ctx context.Context, platform string, current Credentials) (Credentials, error) {
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {current.RefreshToken}}
	if platform == "openai" {
		values.Set("scope", "openid profile email")
	}
	next, err := c.token(ctx, platform, values)
	if err != nil {
		return Credentials{}, err
	}
	if next.RefreshToken == "" {
		next.RefreshToken = current.RefreshToken
	}
	if next.AccountID == "" {
		next.AccountID = current.AccountID
	}
	if platform == "openai" && next.AccountID != current.AccountID {
		return Credentials{}, ErrToken
	}
	next.ProjectID = current.ProjectID
	if !next.Valid(platform) {
		return Credentials{}, ErrToken
	}
	return next, nil
}

func accountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Auth struct {
			ID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return claims.Auth.ID
}

type session struct {
	Owner, Platform, Verifier, State string
	Expires                          time.Time
}
type Sessions struct {
	mu    sync.Mutex
	items map[string]session
}
type Authorization struct {
	SessionID string    `json:"session_id"`
	AuthURL   string    `json:"auth_url"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
}

func nonce() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (s *Sessions) Start(owner, platform string) (Authorization, error) {
	cfg, err := authorizationConfiguration(platform)
	if err != nil {
		return Authorization{}, err
	}
	id, err := nonce()
	if err != nil {
		return Authorization{}, err
	}
	state, err := nonce()
	if err != nil {
		return Authorization{}, err
	}
	verifier, err := nonce()
	if err != nil {
		return Authorization{}, err
	}
	hash := sha256.Sum256([]byte(verifier))
	query := url.Values{"client_id": {cfg.ClientID}, "redirect_uri": {cfg.RedirectURI}, "scope": {cfg.Scope}, "response_type": {"code"}, "state": {state}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}}
	switch platform {
	case "openai":
		query.Set("id_token_add_organizations", "true")
		query.Set("codex_cli_simplified_flow", "true")
	case "anthropic":
		query.Set("code", "true")
	case "gemini", "antigravity":
		query.Set("access_type", "offline")
		query.Set("prompt", "consent")
	}
	expires := time.Now().Add(10 * time.Minute)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = make(map[string]session)
	}
	for key, item := range s.items {
		if time.Now().After(item.Expires) {
			delete(s.items, key)
		}
	}
	if len(s.items) >= 1024 {
		return Authorization{}, ErrSession
	}
	s.items[id] = session{owner, platform, verifier, state, expires}
	return Authorization{id, cfg.AuthorizeURL + "?" + query.Encode(), state, expires}, nil
}

// Consume verifies ownership and state before consuming the single-use session.
func (s *Sessions) Consume(owner, platform, id, input, state string) (code, verifier, verifiedState string, err error) {
	code = strings.TrimSpace(input)
	if len(code) > 16384 || len(state) > 256 {
		return "", "", "", ErrSession
	}
	if parsed, parseErr := url.Parse(code); parseErr == nil && parsed.IsAbs() {
		if parsed.Query().Get("error") != "" {
			return "", "", "", ErrSession
		}
		code, state = parsed.Query().Get("code"), parsed.Query().Get("state")
	} else if before, after, ok := strings.Cut(code, "#"); ok {
		code, state = before, after
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok || item.Owner != owner || item.Platform != platform || time.Now().After(item.Expires) || code == "" || subtle.ConstantTimeCompare([]byte(state), []byte(item.State)) != 1 {
		return "", "", "", ErrSession
	}
	delete(s.items, id)
	return code, item.Verifier, item.State, nil
}

func Owner(user, org uint64, role string) string { return fmt.Sprintf("%d:%d:%s", user, org, role) }
