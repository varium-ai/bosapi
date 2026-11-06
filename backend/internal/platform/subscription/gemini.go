package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var ErrProject = errors.New("Google subscription project unavailable; supply the account's Google Cloud project ID and authorize again")

type projectRequestError struct{ message string }

func (e *projectRequestError) Error() string { return e.message }
func (e *projectRequestError) Unwrap() error { return ErrProject }

func projectRequestFailure(action, detail string) error {
	return &projectRequestError{message: fmt.Sprintf("Google Code Assist %s failed: %s; start official account sign-in again", action, detail)}
}

// Only expose known diagnostic codes, never Google's message, metadata, or raw
// response: those may contain account information or echoed credentials.
type googleProjectStatus struct {
	Code    int    `json:"code"`
	Status  string `json:"status"`
	Details []struct {
		Reason string `json:"reason"`
	} `json:"details"`
}

func googleProjectStatusFailure(action, detail string, status googleProjectStatus) error {
	if status.Status == "" {
		status.Status = map[int]string{3: "INVALID_ARGUMENT", 5: "NOT_FOUND", 7: "PERMISSION_DENIED", 8: "RESOURCE_EXHAUSTED", 9: "FAILED_PRECONDITION", 13: "INTERNAL", 14: "UNAVAILABLE", 16: "UNAUTHENTICATED"}[status.Code]
	}
	switch status.Status {
	case "INVALID_ARGUMENT", "UNAUTHENTICATED", "PERMISSION_DENIED", "NOT_FOUND", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "INTERNAL", "UNAVAILABLE", "DEADLINE_EXCEEDED":
		detail += " " + status.Status
	}
	for _, item := range status.Details {
		switch item.Reason {
		case "SERVICE_DISABLED":
			detail += "; SERVICE_DISABLED: enable the Gemini for Google Cloud API for the account's project"
		case "ACCESS_TOKEN_SCOPE_INSUFFICIENT":
			detail += "; ACCESS_TOKEN_SCOPE_INSUFFICIENT: grant the required Google permissions during sign-in"
		case "BILLING_DISABLED":
			detail += "; BILLING_DISABLED: check billing for the selected Google Cloud project"
		case "CONSUMER_INVALID":
			detail += "; CONSUMER_INVALID: check the account's Google Cloud project ID"
		default:
			continue
		}
		break
	}
	return projectRequestFailure(action, detail)
}

func googleProjectFailure(action string, res *http.Response) error {
	var body struct {
		Error googleProjectStatus `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body)
	return googleProjectStatusFailure(action, fmt.Sprintf("HTTP %d", res.StatusCode), body.Error)
}

func (c Client) codeAssist(ctx context.Context, platform, token, action string, payload any, result any) error {
	cfg, err := Configuration(platform)
	if err != nil || platform != "gemini" && platform != "antigravity" {
		return ErrProject
	}
	return c.googleProjectRequest(ctx, platform, token, http.MethodPost, cfg.BaseURL+"/v1internal:"+action, action, payload, result)
}

func (c Client) googleProjectRequest(ctx context.Context, platform, token, method, endpoint, action string, payload any, result any) error {
	var raw []byte
	var err error
	if payload != nil {
		raw, err = json.Marshal(payload)
	}
	if err != nil {
		return ErrProject
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return ErrProject
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if platform == "antigravity" {
		req.Header.Set("User-Agent", AntigravityUserAgent)
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		return projectRequestFailure(action, "network request failed; check the server's connection to Google")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return googleProjectFailure(action, res)
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(result) != nil {
		return projectRequestFailure(action, "invalid Google response (HTTP 200)")
	}
	return nil
}

type codeAssistAccount struct {
	Project     string          `json:"cloudaicompanionProject"`
	CurrentTier json.RawMessage `json:"currentTier"`
	PaidTier    json.RawMessage `json:"paidTier"`
	Allowed     []struct {
		ID      string `json:"id"`
		Default bool   `json:"isDefault"`
	} `json:"allowedTiers"`
}

type codeAssistOnboarding struct {
	Name     string               `json:"name"`
	Done     bool                 `json:"done"`
	Error    *googleProjectStatus `json:"error"`
	Response struct {
		Project json.RawMessage `json:"cloudaicompanionProject"`
	} `json:"response"`
}

var codeAssistOperationName = regexp.MustCompile(`^operations/[a-zA-Z0-9_/-]+$`)

func (c Client) pollCodeAssist(ctx context.Context, platform, token, name string, result *codeAssistOnboarding) error {
	if len(name) > 512 || !codeAssistOperationName.MatchString(name) {
		return projectRequestFailure("onboardUser", "Google returned an invalid operation name")
	}
	cfg, err := Configuration(platform)
	if err != nil {
		return err
	}
	return c.googleProjectRequest(ctx, platform, token, http.MethodGet, cfg.BaseURL+"/v1internal/"+name, "getOperation", nil, result)
}

// CodeAssistProject resolves the platform's managed project or onboards a new user.
func (c Client) CodeAssistProject(ctx context.Context, platform, token, project string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	project = strings.TrimSpace(project)
	if len(project) > 128 || strings.ContainsAny(project, "\r\n/\\") {
		return "", ErrProject
	}
	metadata := map[string]string{"ideType": "GEMINI_CLI", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}
	if platform == "antigravity" {
		metadata = map[string]string{"ideType": "ANTIGRAVITY", "ideName": "antigravity", "ideVersion": strings.TrimPrefix(AntigravityUserAgent, "antigravity/hub/")}
	}
	payload := map[string]any{"metadata": metadata}
	if project != "" {
		payload["cloudaicompanionProject"] = project
	}
	var loaded codeAssistAccount
	if err := c.codeAssist(ctx, platform, token, "loadCodeAssist", payload, &loaded); err != nil {
		return "", err
	}
	if loaded.Project != "" {
		return loaded.Project, nil
	}
	if project != "" {
		return project, nil
	}
	if len(loaded.CurrentTier) > 0 && string(loaded.CurrentTier) != "null" || len(loaded.PaidTier) > 0 && string(loaded.PaidTier) != "null" {
		return c.googleCloudProject(ctx, platform, token)
	}
	tier := "free-tier"
	for _, candidate := range loaded.Allowed {
		if candidate.Default {
			tier = candidate.ID
			break
		}
	}
	onboardMetadata := map[string]string{"ideType": metadata["ideType"], "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}
	operationName := ""
	for attempt := 0; attempt < 5; attempt++ {
		var result codeAssistOnboarding
		var err error
		if operationName != "" {
			err = c.pollCodeAssist(ctx, platform, token, operationName, &result)
		} else {
			err = c.codeAssist(ctx, platform, token, "onboardUser", map[string]any{"tierId": tier, "metadata": onboardMetadata}, &result)
		}
		if err != nil {
			return "", err
		}
		if result.Error != nil {
			return "", googleProjectStatusFailure("onboardUser", fmt.Sprintf("Google operation error (code %d)", result.Error.Code), *result.Error)
		}
		if result.Done {
			var name string
			if json.Unmarshal(result.Response.Project, &name) == nil && name != "" {
				return name, nil
			}
			var object struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(result.Response.Project, &object) == nil && object.ID != "" {
				return object.ID, nil
			}
			// A completed operation does not always contain the project. Reload
			// account state before looking for an existing Cloud project.
			var refreshed codeAssistAccount
			if err := c.codeAssist(ctx, platform, token, "loadCodeAssist", payload, &refreshed); err != nil {
				return "", err
			}
			if project := strings.TrimSpace(refreshed.Project); project != "" {
				return project, nil
			}
			return c.googleCloudProject(ctx, platform, token)
		}
		if result.Name != "" {
			operationName = result.Name
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", projectRequestFailure("onboardUser", "Google project initialization did not complete in time")
}

// googleCloudProject only discovers existing projects. It never creates a
// project, enables APIs, or changes billing. Ambiguous matches require a manual ID.
func (c Client) googleCloudProject(ctx context.Context, platform, token string) (string, error) {
	type cloudProject struct {
		ID    string `json:"projectId"`
		Name  string `json:"name"`
		State string `json:"lifecycleState"`
	}
	projects := map[string]cloudProject{}
	pageToken := ""
	seen := map[string]bool{}
	for page := 0; page < 10; page++ {
		query := url.Values{"pageSize": {"100"}, "filter": {"lifecycleState:ACTIVE"}}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var result struct {
			Projects []cloudProject `json:"projects"`
			Next     string         `json:"nextPageToken"`
		}
		if err := c.googleProjectRequest(ctx, platform, token, http.MethodGet, "https://cloudresourcemanager.googleapis.com/v1/projects?"+query.Encode(), "Cloud Resource Manager projects.list", nil, &result); err != nil {
			return "", err
		}
		for _, project := range result.Projects {
			project.ID = strings.TrimSpace(project.ID)
			if project.State == "ACTIVE" && project.ID != "" && len(project.ID) <= 128 && !strings.ContainsAny(project.ID, "\r\n/\\") {
				projects[project.ID] = project
			}
		}
		pageToken = result.Next
		if pageToken == "" {
			break
		}
		if seen[pageToken] || page == 9 {
			return "", projectRequestFailure("Cloud Resource Manager projects.list", "project listing is incomplete; supply the account's Google Cloud project ID")
		}
		seen[pageToken] = true
	}
	best, bestScore, count := "", -1, 0
	for _, project := range projects {
		id, name := strings.ToLower(project.ID), strings.ToLower(project.Name)
		score := 0
		if strings.Contains(id, "default") || strings.Contains(name, "default") {
			score = 1
		}
		if strings.Contains(id, "cloud-ai-companion") || strings.Contains(name, "cloud ai companion") || strings.Contains(name, "code assist") {
			score = 2
		}
		if score > bestScore {
			best, bestScore, count = project.ID, score, 1
		} else if score == bestScore {
			count++
		}
	}
	if count == 0 {
		return "", projectRequestFailure("project discovery", "Google returned no managed project and no accessible ACTIVE Google Cloud project; supply the account's Google Cloud project ID")
	}
	if count > 1 {
		return "", projectRequestFailure("project discovery", "multiple Google Cloud projects are available; select one by supplying its project ID")
	}
	return best, nil
}
