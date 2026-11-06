package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestCodeAssistProjectReportsSafeFailureDetails(t *testing.T) {
	for _, platform := range []string{"gemini", "antigravity"} {
		for _, test := range []struct {
			name   string
			status int
			body   string
			err    error
			want   string
		}{
			{"service disabled", 403, `{"error":{"status":"PERMISSION_DENIED","message":"private-access private-account","details":[{"reason":"SERVICE_DISABLED","metadata":{"private":"private-refresh"}}]}}`, nil, "HTTP 403 PERMISSION_DENIED; SERVICE_DISABLED"},
			{"scope missing", 403, `{"error":{"status":"PERMISSION_DENIED","details":[{"reason":"ACCESS_TOKEN_SCOPE_INSUFFICIENT"}]}}`, nil, "ACCESS_TOKEN_SCOPE_INSUFFICIENT"},
			{"rate limited", 429, `{"error":{"status":"RESOURCE_EXHAUSTED","message":"private-access"}}`, nil, "HTTP 429 RESOURCE_EXHAUSTED"},
			{"unknown codes", 500, `{"error":{"status":"private-access","details":[{"reason":"private-refresh"}]}}`, nil, "HTTP 500"},
			{"non JSON error", 502, "private-access private-refresh", nil, "HTTP 502"},
			{"invalid success response", 200, `{"cloudaicompanionProject":{"private":"private-access"}}`, nil, "invalid Google response (HTTP 200)"},
			{"network", 0, "", errors.New("private-access in transport error"), "network request failed"},
		} {
			t.Run(platform+"/"+test.name, func(t *testing.T) {
				client := Client{HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
					if test.err != nil {
						return nil, test.err
					}
					res := response(test.body)
					res.StatusCode = test.status
					return res, nil
				})}}
				_, err := client.CodeAssistProject(context.Background(), platform, "private-access", "")
				if !errors.Is(err, ErrProject) || !strings.Contains(err.Error(), "loadCodeAssist") || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "sign-in again") {
					t.Fatalf("expected actionable project error, got %v", err)
				}
				for _, private := range []string{"private-access", "private-refresh", "private-account"} {
					if strings.Contains(err.Error(), private) {
						t.Fatal("project error exposed upstream private information")
					}
				}
			})
		}
	}
}

func TestCodeAssistProjectReportsOnboardingStage(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"upstream failure", `{"error":{"status":"INVALID_ARGUMENT"}}`, "HTTP 400 INVALID_ARGUMENT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := Client{HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				if strings.HasSuffix(req.URL.Path, "loadCodeAssist") {
					return response(`{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`), nil
				}
				res := response(test.body)
				if test.name == "upstream failure" {
					res.StatusCode = 400
				}
				return res, nil
			})}}
			_, err := client.CodeAssistProject(context.Background(), "gemini", "token", "")
			if !errors.Is(err, ErrProject) || !strings.Contains(err.Error(), "onboardUser") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected onboarding failure, got %v", err)
			}
		})
	}
}

func TestCodeAssistProjectRecoversMissingOnboardingProject(t *testing.T) {
	for _, platform := range []string{"gemini", "antigravity"} {
		for _, reloaded := range []bool{false, true} {
			t.Run(platform+map[bool]string{true: "/reload", false: "/resource-manager"}[reloaded], func(t *testing.T) {
				loads, onboards, listings := 0, 0, 0
				client := Client{HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
					if req.Header.Get("Authorization") != "Bearer account-token" {
						t.Fatal("account token missing")
					}
					if req.URL.Host == "cloudresourcemanager.googleapis.com" {
						listings++
						if req.Method != http.MethodGet || req.URL.Path != "/v1/projects" || req.URL.Query().Get("filter") != "lifecycleState:ACTIVE" {
							t.Fatal("incorrect Resource Manager request")
						}
						return response(`{"projects":[{"projectId":"deleted","lifecycleState":"DELETE_REQUESTED"},{"projectId":"existing-project","lifecycleState":"ACTIVE"}]}`), nil
					}
					switch req.URL.Path {
					case "/v1internal:loadCodeAssist":
						loads++
						if reloaded && loads == 2 {
							return response(`{"cloudaicompanionProject":"existing-project"}`), nil
						}
						return response(`{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`), nil
					case "/v1internal:onboardUser":
						onboards++
						return response(`{"done":true,"response":{}}`), nil
					default:
						t.Fatalf("unexpected Google request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}
				})}}
				project, err := client.CodeAssistProject(context.Background(), platform, "account-token", "")
				if err != nil || project != "existing-project" || loads != 2 || onboards != 1 || reloaded && listings != 0 || !reloaded && listings != 1 {
					t.Fatalf("project=%q loads=%d onboards=%d listings=%d err=%v", project, loads, onboards, listings, err)
				}
			})
		}
	}
}

func TestCodeAssistProjectDoesNotTreatFailedOperationAsSuccess(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1internal:loadCodeAssist":
			return response(`{}`), nil
		case "/v1internal:onboardUser":
			return response(`{"done":true,"error":{"code":7,"message":"private-account private-token","details":[{"reason":"SERVICE_DISABLED"}]}}`), nil
		default:
			t.Fatal("must not fall back after Google reports an operation error")
			return nil, nil
		}
	})}}
	_, err := client.CodeAssistProject(context.Background(), "gemini", "account-token", "")
	if !errors.Is(err, ErrProject) || !strings.Contains(err.Error(), "code 7") || !strings.Contains(err.Error(), "PERMISSION_DENIED") || !strings.Contains(err.Error(), "SERVICE_DISABLED") || strings.Contains(err.Error(), "private-") {
		t.Fatalf("operation error lost or leaked details: %v", err)
	}
}

func TestCodeAssistProjectPollsExistingOperation(t *testing.T) {
	onboards, polls := 0, 0
	client := Client{HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1internal:loadCodeAssist":
			return response(`{}`), nil
		case "/v1internal:onboardUser":
			onboards++
			return response(`{"name":"operations/test-operation","done":false}`), nil
		case "/v1internal/operations/test-operation":
			polls++
			if req.Method != http.MethodGet {
				t.Fatal("operation polling must use GET")
			}
			return response(`{"done":true,"response":{"cloudaicompanionProject":{"id":"managed-project"}}}`), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.Path)
			return nil, nil
		}
	})}}
	project, err := client.CodeAssistProject(context.Background(), "gemini", "token", "")
	if err != nil || project != "managed-project" || onboards != 1 || polls != 1 {
		t.Fatalf("project=%q onboards=%d polls=%d error=%v", project, onboards, polls, err)
	}
}

func TestGoogleCloudProjectSelectionAndPagination(t *testing.T) {
	for _, test := range []struct {
		name  string
		pages []string
		want  string
	}{
		{"none", []string{`{"projects":[]}`}, ""},
		{"ambiguous", []string{`{"projects":[{"projectId":"one","lifecycleState":"ACTIVE"},{"projectId":"two","lifecycleState":"ACTIVE"}]}`}, ""},
		{"companion preferred", []string{`{"projects":[{"projectId":"default-project","lifecycleState":"ACTIVE"},{"projectId":"companion","name":"Gemini Code Assist","lifecycleState":"ACTIVE"}]}`}, "companion"},
		{"pagination and duplicates", []string{`{"projects":[{"projectId":"default-project","lifecycleState":"ACTIVE"}],"nextPageToken":"next-page"}`, `{"projects":[{"projectId":"default-project","lifecycleState":"ACTIVE"}]}`}, "default-project"},
		{"later ambiguity", []string{`{"projects":[{"projectId":"one","lifecycleState":"ACTIVE"}],"nextPageToken":"next-page"}`, `{"projects":[{"projectId":"two","lifecycleState":"ACTIVE"}]}`}, ""},
		{"page loop", []string{`{"projects":[],"nextPageToken":"next-page"}`, `{"projects":[],"nextPageToken":"next-page"}`}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			page := 0
			client := Client{HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				if page >= len(test.pages) {
					t.Fatal("unexpected page request")
				}
				if page > 0 && req.URL.Query().Get("pageToken") != "next-page" {
					t.Fatal("pagination token missing")
				}
				res := response(test.pages[page])
				page++
				return res, nil
			})}}
			got, err := client.googleCloudProject(context.Background(), "gemini", "token")
			if got != test.want || test.want == "" && !errors.Is(err, ErrProject) || test.want != "" && err != nil || page != len(test.pages) {
				t.Fatalf("project=%q pages=%d err=%v", got, page, err)
			}
		})
	}
}

func TestCodeAssistExistingProjectFallbackAndManualOverride(t *testing.T) {
	for _, manual := range []string{"", "manual-project"} {
		t.Run(manual, func(t *testing.T) {
			listings := 0
			client := Client{HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "cloudresourcemanager.googleapis.com" {
					listings++
					return response(`{"projects":[{"projectId":"existing-project","lifecycleState":"ACTIVE"}]}`), nil
				}
				if req.URL.Path != "/v1internal:loadCodeAssist" {
					t.Fatal("must not onboard an existing subscriber")
				}
				var body map[string]any
				if json.NewDecoder(req.Body).Decode(&body) != nil || manual != "" && body["cloudaicompanionProject"] != manual {
					t.Fatal("manual project missing")
				}
				return response(`{"paidTier":{"id":"paid"}}`), nil
			})}}
			got, err := client.CodeAssistProject(context.Background(), "gemini", "token", manual)
			want := manual
			if manual == "" {
				want = "existing-project"
			}
			if err != nil || got != want || manual != "" && listings != 0 || manual == "" && listings != 1 {
				t.Fatalf("project=%q listings=%d err=%v", got, listings, err)
			}
		})
	}
}
