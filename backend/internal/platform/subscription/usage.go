package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"
)

var ErrUsageUnavailable = errors.New("subscription usage is unavailable")

// UsageStatusError reports an upstream status without including the response body.
type UsageStatusError int

func (e UsageStatusError) Error() string { return "subscription usage HTTP " + strconv.Itoa(int(e)) }

type UsageSnapshot struct {
	CheckedAt time.Time     `json:"checked_at"`
	Windows   []UsageWindow `json:"windows"`
}

type UsageWindow struct {
	Label            string     `json:"label,omitempty"`
	Period           string     `json:"period,omitempty"`
	Unit             string     `json:"unit,omitempty"`
	DurationSeconds  int64      `json:"duration_seconds,omitempty"`
	UsedPercent      float64    `json:"used_percent"`
	RemainingPercent float64    `json:"remaining_percent"`
	ResetsAt         *time.Time `json:"resets_at,omitempty"`
}

func (s *UsageSnapshot) add(window UsageWindow, percent *float64, remainingFraction bool) {
	if percent == nil {
		return
	}
	value := *percent
	if remainingFraction {
		value *= 100
		if value < 0 || value > 100 {
			return
		}
		window.RemainingPercent = value
		window.UsedPercent = 100 - value
	} else {
		if value < 0 {
			return
		}
		window.UsedPercent = value
		window.RemainingPercent = max(0, 100-value)
	}
	s.Windows = append(s.Windows, window)
}

func usageReset(raw string) *time.Time {
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return &t
	}
	return nil
}

func SupportsUsage(platform string) bool {
	switch platform {
	case "openai", "anthropic", "gemini", "antigravity":
		return true
	default:
		return false
	}
}

// Usage queries the account's quota using fixed upstream endpoints. An empty
// snapshot means the upstream response did not contain a measured window.
func (c Client) Usage(ctx context.Context, platform string, cred Credentials) (UsageSnapshot, error) {
	cfg, err := Configuration(platform)
	if err != nil || !SupportsUsage(platform) {
		return UsageSnapshot{}, ErrUsageUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	snapshot := UsageSnapshot{Windows: []UsageWindow{}}

	switch platform {
	case "openai":
		err = c.codexUsage(ctx, cred, &snapshot)
	case "anthropic":
		err = c.claudeUsage(ctx, cfg.BaseURL, cred, &snapshot)
	case "gemini":
		var data struct {
			Buckets []struct {
				Model string   `json:"modelId"`
				Unit  string   `json:"tokenType"`
				Left  *float64 `json:"remainingFraction"`
				Reset string   `json:"resetTime"`
			} `json:"buckets"`
		}
		err = c.googleUsage(ctx, cfg.BaseURL, "retrieveUserQuota", platform, cred, &data)
		for _, b := range data.Buckets {
			if b.Model != "" {
				snapshot.add(UsageWindow{Label: b.Model, Unit: b.Unit, ResetsAt: usageReset(b.Reset)}, b.Left, true)
			}
		}
	case "antigravity":
		err = c.antigravityUsage(ctx, cfg.BaseURL, cred, &snapshot)
	}
	if err != nil {
		return UsageSnapshot{}, err
	}
	snapshot.CheckedAt = time.Now().UTC()
	return snapshot, nil
}

func (c Client) codexUsage(ctx context.Context, cred Credentials, snapshot *UsageSnapshot) error {
	type window struct {
		Used       *float64 `json:"used_percent"`
		Seconds    int64    `json:"limit_window_seconds"`
		Reset      int64    `json:"reset_at"`
		ResetAfter *int64   `json:"reset_after_seconds"`
	}
	type limits struct {
		Primary   window `json:"primary_window"`
		Secondary window `json:"secondary_window"`
	}
	var data struct {
		RateLimit  limits `json:"rate_limit"`
		Additional []struct {
			Name   string `json:"limit_name"`
			Limits limits `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	headers := make(http.Header)
	headers.Set("ChatGPT-Account-ID", cred.AccountID)
	headers.Set("OpenAI-Beta", "codex-1")
	ApplyCodexClientHeaders(headers)
	if err := c.readUsage(ctx, http.MethodGet, "https://chatgpt.com/backend-api/wham/usage", cred.AccessToken, headers, nil, &data); err != nil {
		return err
	}
	now := time.Now().UTC()
	appendLimits := func(label string, quota limits) {
		for _, w := range []window{quota.Primary, quota.Secondary} {
			item := UsageWindow{Label: label, DurationSeconds: w.Seconds}
			if w.Reset > 0 && w.Reset < 253402300800 {
				reset := time.Unix(w.Reset, 0).UTC()
				item.ResetsAt = &reset
			} else if w.ResetAfter != nil && *w.ResetAfter >= 0 && *w.ResetAfter <= int64((365*24*time.Hour)/time.Second) {
				reset := now.Add(time.Duration(*w.ResetAfter) * time.Second)
				item.ResetsAt = &reset
			}
			snapshot.add(item, w.Used, false)
		}
	}
	appendLimits("", data.RateLimit)
	for _, extra := range data.Additional {
		if extra.Name != "" {
			appendLimits(extra.Name, extra.Limits)
		}
	}
	return nil
}

func (c Client) claudeUsage(ctx context.Context, baseURL string, cred Credentials, snapshot *UsageSnapshot) error {
	var data map[string]json.RawMessage
	headers := make(http.Header)
	headers.Set("anthropic-beta", "oauth-2025-04-20")
	if err := c.readUsage(ctx, http.MethodGet, baseURL+"/api/oauth/usage", cred.AccessToken, headers, nil, &data); err != nil {
		return err
	}
	for _, item := range []struct{ key, label, period string }{
		{"five_hour", "", "5h"}, {"seven_day", "", "weekly"},
		{"seven_day_sonnet", "Sonnet", "weekly"}, {"seven_day_opus", "Opus", "weekly"},
		{"seven_day_overage_included", "Fable", "weekly"},
	} {
		var w struct {
			Used  *float64 `json:"utilization"`
			Reset string   `json:"resets_at"`
		}
		if raw, ok := data[item.key]; ok && json.Unmarshal(raw, &w) == nil {
			snapshot.add(UsageWindow{Label: item.label, Period: item.period, ResetsAt: usageReset(w.Reset)}, w.Used, false)
		}
	}
	return nil
}

type googleQuota struct {
	Left  *float64 `json:"remainingFraction"`
	Reset string   `json:"resetTime"`
}

func (c Client) antigravityUsage(ctx context.Context, baseURL string, cred Credentials, snapshot *UsageSnapshot) error {
	var summary struct {
		Groups []struct {
			Name    string `json:"displayName"`
			Buckets []struct {
				ID     string `json:"bucketId"`
				Period string `json:"window"`
				googleQuota
			} `json:"buckets"`
		} `json:"groups"`
	}
	err := c.googleUsage(ctx, baseURL, "retrieveUserQuotaSummary", "antigravity", cred, &summary)
	if err != nil && err != UsageStatusError(404) && err != UsageStatusError(405) && err != UsageStatusError(501) {
		return err
	}
	for _, group := range summary.Groups {
		for _, bucket := range group.Buckets {
			label := group.Name
			if label == "" {
				label = bucket.ID
			}
			snapshot.add(UsageWindow{Label: label, Period: bucket.Period, ResetsAt: usageReset(bucket.Reset)}, bucket.Left, true)
		}
	}
	if len(snapshot.Windows) > 0 {
		return nil
	}
	var catalog struct {
		Models map[string]struct {
			Quota googleQuota `json:"quotaInfo"`
		} `json:"models"`
	}
	if err := c.googleUsage(ctx, baseURL, "fetchAvailableModels", "antigravity", cred, &catalog); err != nil {
		return err
	}
	names := make([]string, 0, len(catalog.Models))
	for name := range catalog.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		quota := catalog.Models[name].Quota
		snapshot.add(UsageWindow{Label: name, ResetsAt: usageReset(quota.Reset)}, quota.Left, true)
	}
	return nil
}

func (c Client) googleUsage(ctx context.Context, baseURL, action, platform string, cred Credentials, result any) error {
	headers := make(http.Header)
	if platform == "antigravity" {
		headers.Set("User-Agent", AntigravityUserAgent)
	}
	payload, _ := json.Marshal(map[string]string{"project": cred.ProjectID})
	return c.readUsage(ctx, http.MethodPost, baseURL+"/v1internal:"+action, cred.AccessToken, headers, payload, result)
}

func (c Client) readUsage(ctx context.Context, method, endpoint, token string, headers http.Header, payload []byte, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return ErrUsageUnavailable
	}
	req.Header = headers
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return ErrUsageUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return UsageStatusError(res.StatusCode)
	}
	const limit = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if err != nil || len(raw) > limit || json.Unmarshal(raw, result) != nil {
		return ErrUsageUnavailable
	}
	return nil
}
