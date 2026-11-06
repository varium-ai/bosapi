package interfaces

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/auth/application"
	"bosapi/backend/internal/auth/domain"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

func TestSecureCookieBehindHTTPSProxy(t *testing.T) {
	for _, test := range []struct {
		name      string
		forwarded string
		tls       bool
		want      bool
	}{
		{name: "plain HTTP", want: false},
		{name: "HTTPS proxy", forwarded: "https", want: true},
		{name: "direct TLS", tls: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/", nil)
			request.Header.Set("X-Forwarded-Proto", test.forwarded)
			if test.tls {
				request.TLS = &tls.ConnectionState{}
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = request
			if got := secureCookie(ctx); got != test.want {
				t.Fatalf("secureCookie() = %t, want %t", got, test.want)
			}
		})
	}
}

type stubAuthService struct {
	user    domain.User
	err     error
	reasons []string
}

func (s *stubAuthService) Login(context.Context, string, string) (application.LoginResponse, error) {
	return application.LoginResponse{}, s.err
}
func (s *stubAuthService) VerifyMFA(context.Context, string, string) (application.TokenPair, error) {
	return application.TokenPair{}, nil
}
func (s *stubAuthService) SetupMFA(context.Context, uint64) (application.MFASetupResult, error) {
	return application.MFASetupResult{}, nil
}
func (s *stubAuthService) EnableMFA(context.Context, uint64, string) error {
	return nil
}
func (s *stubAuthService) DisableMFA(context.Context, uint64, string, string) error {
	return nil
}
func (s *stubAuthService) Refresh(context.Context, string) (application.TokenPair, error) {
	return application.TokenPair{}, nil
}
func (s *stubAuthService) Logout(context.Context, string, string, time.Time) error {
	return nil
}
func (s *stubAuthService) VerifyAccess(context.Context, string) (domain.User, string, time.Time, error) {
	if s.err != nil {
		return domain.User{}, "", time.Time{}, s.err
	}
	return s.user, "test-jti", time.Now().Add(time.Hour), nil
}
func (s *stubAuthService) ChangePassword(context.Context, uint64, string, string) error {
	return nil
}
func (s *stubAuthService) RequestPasswordReset(context.Context, string) (int, string, error) {
	return 0, "", nil
}
func (s *stubAuthService) ResetPasswordWithOTP(context.Context, string, string, string) error {
	return nil
}
func (s *stubAuthService) RecordLoginAttempt(_ context.Context, _ string, _ string, _ bool, reason string, _ time.Time) error {
	s.reasons = append(s.reasons, reason)
	return nil
}

func TestMFAStatusEndpoint(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	svc := &stubAuthService{
		user: domain.User{
			ID:             1,
			Email:          "admin@example.com",
			OrganizationID: 1,
			Role:           "super_admin",
			Status:         "active",
			TOTPEnabled:    true,
			TOTPEnabledAt:  &now,
		},
	}
	handler := NewHandler(svc)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	api := router.Group("/api/v1/auth")
	api.Use(handler.Authenticate())
	handler.RegisterProtectedRoutes(api)

	t.Run("unauthenticated GET /api/v1/auth/mfa returns 401", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/auth/mfa", nil)
		router.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("authenticated GET /api/v1/auth/mfa returns 200 with status", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/auth/mfa", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Data struct {
				TOTPEnabled   bool       `json:"totp_enabled"`
				TOTPEnabledAt *time.Time `json:"totp_enabled_at"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json unmarshal failed: %v", err)
		}
		if !resp.Data.TOTPEnabled {
			t.Fatalf("expected totp_enabled to be true")
		}
		if resp.Data.TOTPEnabledAt == nil || !resp.Data.TOTPEnabledAt.Equal(now) {
			t.Fatalf("expected totp_enabled_at %v, got %v", now, resp.Data.TOTPEnabledAt)
		}
	})

	t.Run("authenticated GET /api/v1/auth/mfa/status alias returns 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/auth/mfa/status", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("authenticated GET /api/v1/auth/me includes totp_enabled_at", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Data struct {
				TOTPEnabled   bool       `json:"totp_enabled"`
				TOTPEnabledAt *time.Time `json:"totp_enabled_at"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json unmarshal failed: %v", err)
		}
		if !resp.Data.TOTPEnabled || resp.Data.TOTPEnabledAt == nil || !resp.Data.TOTPEnabledAt.Equal(now) {
			t.Fatalf("expected me response to contain totp_enabled=true and totp_enabled_at=%v, got %+v", now, resp.Data)
		}
	})
}

func TestLoginResponseHidesAccountExistenceAndLockState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		err        error
		wantReason string
	}{
		{name: "missing account", err: application.ErrInvalidCredentials, wantReason: "invalid_credentials"},
		{name: "locked account", err: application.ErrLoginLocked, wantReason: "login_locked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &stubAuthService{err: test.err}
			handler := NewHandler(service)
			router := gin.New()
			router.Use(httpapi.Middleware(1024))
			handler.RegisterPublicRoutes(router.Group("/api/v1/auth"))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"missing@example.com","password":"wrong-secret-1234"}`)) // gitleaks:allow -- Synthetic test credential.
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var response struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Error.Code != "unauthorized" || response.Error.Message != "email or password is invalid" {
				t.Fatalf("response=%s", rec.Body.String())
			}
			if len(service.reasons) != 1 || service.reasons[0] != test.wantReason {
				t.Fatalf("audit reasons=%v", service.reasons)
			}
		})
	}
}
