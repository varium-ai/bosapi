package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

type memorySettingRepository struct {
	data map[string]domain.SystemSetting
}

func (m *memorySettingRepository) Get(ctx context.Context, key string) (*domain.SystemSetting, error) {
	if s, ok := m.data[key]; ok {
		return &s, nil
	}
	return nil, nil
}

func (m *memorySettingRepository) Set(ctx context.Context, key string, valueText string, isSecret bool, updatedBy *uint64) error {
	m.data[key] = domain.SystemSetting{
		SettingKey: key,
		ValueText:  valueText,
		IsSecret:   isSecret,
		UpdatedBy:  updatedBy,
		UpdatedAt:  time.Now(),
	}
	return nil
}

func (m *memorySettingRepository) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *memorySettingRepository) List(ctx context.Context, prefix string) ([]domain.SystemSetting, error) {
	var list []domain.SystemSetting
	for _, s := range m.data {
		list = append(list, s)
	}
	return list, nil
}

func setupTestRouter() (*gin.Engine, *SystemSettingHandler) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	repo := &memorySettingRepository{data: make(map[string]domain.SystemSetting)}
	service := application.NewSystemSettingService(repo)
	handler := NewSystemSettingHandler(service)

	api := router.Group("/api/v1")
	handler.RegisterPublicRoutes(api)

	admin := api.Group("/admin")
	// Mock authentication middleware setting claims
	admin.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{
			UserID:         1,
			OrganizationID: 1,
			Role:           "super_admin",
		})
		ctx.Next()
	})
	handler.RegisterAdminRoutes(admin)

	return router, handler
}

func TestGenericSettingsCannotMutateUsageReporting(t *testing.T) {
	router, _ := setupTestRouter()
	for _, key := range []string{"analytics.usage_reporting", "ANALYTICS.USAGE_REPORTING", "analytics.usage_reporting.installation_id", "ánalytics.usage_reporting"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/admin/settings/"+url.PathEscape(key), bytes.NewBufferString(`{"value_text":"{\"enabled\":true}"}`)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s %s: %d %s", method, key, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestSystemSettingHandler_GatewayEndpoints(t *testing.T) {
	t.Setenv("BOS_GATEWAY_ENDPOINT", "https://current.example.com/v1/")
	router, _ := setupTestRouter()
	assertPublic := func(expected string) {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/config", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("public config: %d %s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Data) != 1 || payload.Data["gateway_endpoint"] != expected {
			t.Fatalf("public config must expose only the selected endpoint: %+v; want %q", payload.Data, expected)
		}
	}
	assertPublic("")
	// Saving settings changes the address used by every public connection surface.
	payload := []byte(`{"endpoint":"http://gateway.example.com/v1/"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/gateway-endpoints", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	assertPublic("http://gateway.example.com")
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/gateway-endpoints", nil))
	var saved struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &saved); err != nil || len(saved.Data) != 1 || saved.Data["endpoint"] != "http://gateway.example.com" {
		t.Fatalf("saved settings did not round-trip: %s err=%v", getRec.Body.String(), err)
	}
	for _, body := range []string{`{"endpoint":"https://user:secret@example.com"}`, `{"endpoint":""}`, `{}`, `{"prod_endpoint":"https://old.example.com","test_endpoint":"https://old-test.example.com"}`} {
		invalid := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/gateway-endpoints", bytes.NewBufferString(body))
		invalid.Header.Set("Content-Type", "application/json")
		invalidRec := httptest.NewRecorder()
		router.ServeHTTP(invalidRec, invalid)
		if invalidRec.Code != http.StatusBadRequest {
			t.Fatalf("invalid configuration was accepted: %d %s", invalidRec.Code, body)
		}
	}
	assertPublic("http://gateway.example.com")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/settings/gateway-endpoints", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d", rec.Code)
	}
	assertPublic("")
}

func TestSystemSettingHandler_GenericSettings(t *testing.T) {
	router, _ := setupTestRouter()

	// 1. Put generic setting
	body := []byte(`{"value_text":"some_config_value","is_secret":false}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/custom.feature_flag", bytes.NewReader(body))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", putRec.Code, putRec.Body.String())
	}

	// 2. Get generic setting
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/custom.feature_flag", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", getRec.Code)
	}

	// 3. Delete generic setting
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/settings/custom.feature_flag", nil)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", delRec.Code)
	}

	// 4. Get after delete should be 404
	getReq4 := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/custom.feature_flag", nil)
	getRec4 := httptest.NewRecorder()
	router.ServeHTTP(getRec4, getReq4)
	if getRec4.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", getRec4.Code)
	}
}

func TestSystemSettingHandler_NotificationSecretsAreMasked(t *testing.T) {
	router, _ := setupTestRouter()
	payload := []byte(`{"provider":"lark","lark_webhook":"https://open.feishu.cn/open-apis/bot/v2/hook/secret"}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/message-notification-settings", bytes.NewReader(payload))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("expected notification config save 200, got %d: %s", putRec.Code, putRec.Body.String())
	}
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/message-notification-settings", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || bytes.Contains(getRec.Body.Bytes(), []byte("/hook/secret")) {
		t.Fatalf("notification secret leaked: status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	if !bytes.Contains(getRec.Body.Bytes(), []byte("/hook/s****t")) {
		t.Fatalf("expected masked_lark_webhook to contain masked token, body=%s", getRec.Body.String())
	}
	genericReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/notifications.message", nil)
	genericRec := httptest.NewRecorder()
	router.ServeHTTP(genericRec, genericReq)
	if genericRec.Code != http.StatusNotFound {
		t.Fatalf("expected generic secret access to be blocked, got %d", genericRec.Code)
	}
}

func TestSystemSettingHandler_GenericWebhookSettings(t *testing.T) {
	router, handler := setupTestRouter()
	const receiver = "https://receiver.example.com/PRIVATE_PATH?token=PRIVATE_QUERY"
	for _, body := range []string{
		`{"provider":"webhook","webhook_url":"` + receiver + `"}`,
		`{"provider":"webhook","webhook_url":""}`,
	} {
		put := httptest.NewRequest(http.MethodPut, "/api/v1/admin/message-notification-settings", bytes.NewBufferString(body))
		put.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, put)
		if result.Code != http.StatusOK {
			t.Fatalf("save failed: %d %s", result.Code, result.Body.String())
		}
		config, err := handler.service.GetMessageNotificationConfig(context.Background())
		if err != nil || config.Provider != "webhook" || config.WebhookURL != receiver {
			t.Fatalf("receiver was not preserved: %v", err)
		}
		get := httptest.NewRecorder()
		router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/admin/message-notification-settings", nil))
		var response struct {
			Data struct {
				Provider         string `json:"provider"`
				HasWebhookURL    bool   `json:"has_webhook_url"`
				MaskedWebhookURL string `json:"masked_webhook_url"`
			}
		}
		if err := json.Unmarshal(get.Body.Bytes(), &response); err != nil || get.Code != http.StatusOK || response.Data.Provider != "webhook" || !response.Data.HasWebhookURL || response.Data.MaskedWebhookURL != "https://receiver.example.com/****" || bytes.Contains(get.Body.Bytes(), []byte("PRIVATE")) {
			t.Fatalf("masked settings are invalid: %d %s", get.Code, get.Body.String())
		}
	}
	for _, body := range []string{
		`{"provider":"other","webhook_url":"https://receiver.example.com"}`,
		`{"provider":"webhook","webhook_url":"http://receiver.example.com/PRIVATE"}`,
		`{"provider":"webhook","webhook_url":"https://127.0.0.1/PRIVATE"}`,
	} {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/message-notification-settings", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, request)
		if result.Code != http.StatusBadRequest || bytes.Contains(result.Body.Bytes(), []byte("PRIVATE")) {
			t.Fatalf("invalid settings were not safely rejected: %d %s", result.Code, result.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/admin/settings/notifications.message", "/api/v1/admin/settings?prefix=notifications"} {
		result := httptest.NewRecorder()
		router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, path, nil))
		if bytes.Contains(result.Body.Bytes(), []byte("PRIVATE")) {
			t.Fatal("generic settings exposed the webhook URL")
		}
	}
}

type smtpTesterStub struct {
	encrypted bool
	err       error
	called    bool
	bounded   bool
}

func (s *smtpTesterStub) TestSMTP(ctx context.Context) (bool, error) {
	s.called = true
	deadline, ok := ctx.Deadline()
	s.bounded = ok && time.Until(deadline) <= 12*time.Second
	return s.encrypted, s.err
}

func TestSMTPConnectionEndpointSanitizesErrorsAndReportsEncryption(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		router, handler := setupTestRouter()
		stub := &smtpTesterStub{encrypted: encrypted}
		handler.smtpTester = stub
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/admin/message-notification-settings/test", nil))
		if response.Code != http.StatusOK || !stub.called || !stub.bounded {
			t.Fatalf("probe failed: %d %s", response.Code, response.Body.String())
		}
		var payload struct {
			Data struct {
				Connected bool `json:"connected"`
				Encrypted bool `json:"encrypted"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if !payload.Data.Connected || payload.Data.Encrypted != encrypted {
			t.Fatalf("incorrect probe result: %s", response.Body.String())
		}
	}
	router, handler := setupTestRouter()
	handler.smtpTester = &smtpTesterStub{err: fmt.Errorf("private-password in server error")}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/admin/message-notification-settings/test", nil))
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "private-password") {
		t.Fatalf("unsafe failure: %d %s", response.Code, response.Body.String())
	}
}
