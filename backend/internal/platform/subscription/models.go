package subscription

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
)

// Models reads the official catalog without sending credentials to custom URLs.
func (c Client) Models(ctx context.Context, platform string, credentials Credentials) ([]byte, error) {
	cfg, err := Configuration(platform)
	if err != nil {
		return nil, err
	}
	endpoint := cfg.BaseURL + "/v1/models"
	if platform == "openai" {
		endpoint = cfg.BaseURL + "/models"
	}
	if platform == "gemini" {
		_, err := c.CodeAssistProject(ctx, platform, credentials.AccessToken, credentials.ProjectID)
		return nil, err
	}
	if platform == "antigravity" {
		var catalog struct {
			Models map[string]json.RawMessage `json:"models"`
		}
		if err := c.codeAssist(ctx, platform, credentials.AccessToken, "fetchAvailableModels", map[string]string{"project": credentials.ProjectID}, &catalog); err != nil {
			return nil, err
		}
		if len(catalog.Models) == 0 {
			return nil, ErrToken
		}
		names := make([]string, 0, len(catalog.Models))
		for name := range catalog.Models {
			names = append(names, name)
		}
		sort.Strings(names)
		items := make([]map[string]string, 0, len(names))
		for _, name := range names {
			items = append(items, map[string]string{"id": name})
		}
		return json.Marshal(map[string]any{"data": items})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrToken
	}
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	if platform == "openai" {
		req.Header.Set("ChatGPT-Account-ID", credentials.AccountID)
		version := ApplyCodexClientHeaders(req.Header)
		query := req.URL.Query()
		query.Set("client_version", version)
		req.URL.RawQuery = query.Encode()
	}
	if platform == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, ErrToken
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, ErrToken
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, ErrToken
	}
	return raw, nil
}
