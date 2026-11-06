package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminapp "bosapi/backend/internal/admin/application"
	admindomain "bosapi/backend/internal/admin/domain"
	userdomain "bosapi/backend/internal/user/domain"
	userinfra "bosapi/backend/internal/user/infrastructure"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type stubKeyPortalRepository struct {
	key    userdomain.APIKey
	active bool
}

type portalSnapshotTestRepository struct {
	admindomain.AdminUsageRepository
	filter admindomain.AdminUsageFilter
	period string
}

func (repository *portalSnapshotTestRepository) AggregateAdminUsageSnapshot(_ context.Context, filter admindomain.AdminUsageFilter, period string) (admindomain.AdminUsageAggregateSnapshot, error) {
	repository.filter, repository.period = filter, period
	return admindomain.AdminUsageAggregateSnapshot{Snapshot: &admindomain.UsageSnapshotMetadata{Status: "generating"}}, nil
}

func TestKeyPortalStatisticsUseScopedSnapshotsWithoutPeriodicAuthenticationQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if err := sqlDatabase.Close(); err != nil {
			t.Error(err)
		}
	})
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("FROM `api_keys` WHERE id = \\? AND status = \\?").
		WithArgs(uint64(134), "active", sqlmock.AnyArg(), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "status", "daily_quota_tokens"}).AddRow(134, 9, "active", 100))
	mock.ExpectQuery("SELECT status FROM `organizations` WHERE id = \\?").WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
	secret := []byte("portal-snapshot-test-secret")
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"api_key_id": 134, "role": keyPortalRole, "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	source := &portalSnapshotTestRepository{}
	handler := NewKeyPortalHandler(userinfra.NewKeyRepository(database), secret, nil, nil, nil)
	handler.WithUsageService(adminapp.NewAdminUsageService(source))
	router := gin.New()
	handler.RegisterRoutes(router.Group("/key-portal"))
	request := httptest.NewRequest(http.MethodGet, "/key-portal/usage/monthly?organization_id=999&key_id=999&refresh=true", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var result struct {
		Data admindomain.AdminUsageAggregateSnapshot
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || result.Data.Snapshot == nil || result.Data.Snapshot.Status != "generating" ||
		source.filter.OrganizationID != 9 || source.filter.APIKeyID != 134 || !source.filter.Refresh || source.period != "monthly" {
		t.Fatalf("body=%s filter=%+v period=%s", response.Body, source.filter, source.period)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func (repository *stubKeyPortalRepository) FindGatewayKey(_ context.Context, rawKey string, _ time.Time) (userdomain.APIKey, bool, error) {
	if rawKey != "sk-test-key-123456" || !repository.active {
		return userdomain.APIKey{}, false, nil
	}
	return repository.key, true, nil
}

func (repository *stubKeyPortalRepository) FindActiveKeyByID(_ context.Context, id uint64, _ time.Time) (userdomain.APIKey, bool, error) {
	if id != repository.key.ID || !repository.active {
		return userdomain.APIKey{}, false, nil
	}
	return repository.key, true, nil
}

func (repository *stubKeyPortalRepository) FindActiveKeyIdentityByID(ctx context.Context, id uint64, now time.Time) (userdomain.APIKey, bool, error) {
	return repository.FindActiveKeyByID(ctx, id, now)
}

func TestKeyPortalSessionRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &stubKeyPortalRepository{
		key:    userdomain.APIKey{ID: 42, OrganizationID: 7, Name: "test key", KeyHash: "hash-v1", KeyPrefix: "sk-test", Status: "active"},
		active: true,
	}
	handler := NewKeyPortalHandler(repository, []byte("portal-test-signing-secret"), nil, nil, nil)
	router := gin.New()
	handler.RegisterPublicRoutes(router.Group("/auth"))
	handler.RegisterRoutes(router.Group("/key-portal"))

	loginRequest := httptest.NewRequest(http.MethodPost, "/auth/key-login", bytes.NewBufferString(`{"api_key":"sk-test-key-123456"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.Header.Set("X-Forwarded-Proto", "https")
	loginResponse := httptest.NewRecorder()
	router.ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var loginEnvelope struct {
		Data struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		} `json:"data"`
	}
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &loginEnvelope); err != nil {
		t.Fatal(err)
	}
	if loginEnvelope.Data.AccessToken == "" || loginEnvelope.Data.ExpiresIn != int(keyPortalAccessTTL.Seconds()) {
		t.Fatalf("unexpected login session: %+v", loginEnvelope.Data)
	}
	refreshCookie := responseCookie(t, loginResponse.Result(), keyPortalRefreshCookie)
	if !refreshCookie.HttpOnly || !refreshCookie.Secure || refreshCookie.SameSite != http.SameSiteStrictMode || refreshCookie.MaxAge != int(keyPortalRefreshTTL.Seconds()) {
		t.Fatalf("refresh cookie is not protected as expected: %+v", refreshCookie)
	}
	accessRequest := httptest.NewRequest(http.MethodGet, "/key-portal/info", nil)
	accessRequest.Header.Set("Authorization", "Bearer "+loginEnvelope.Data.AccessToken)
	accessResponse := httptest.NewRecorder()
	router.ServeHTTP(accessResponse, accessRequest)
	if accessResponse.Code != http.StatusOK {
		t.Fatalf("access token returned %d: %s", accessResponse.Code, accessResponse.Body.String())
	}

	refreshRequest := httptest.NewRequest(http.MethodPost, "/auth/key-refresh", nil)
	refreshRequest.Header.Set("X-Forwarded-Proto", "https")
	refreshRequest.AddCookie(refreshCookie)
	refreshResponse := httptest.NewRecorder()
	router.ServeHTTP(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", refreshResponse.Code, refreshResponse.Body.String())
	}
	var refreshEnvelope struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(refreshResponse.Body.Bytes(), &refreshEnvelope); err != nil {
		t.Fatal(err)
	}
	if refreshEnvelope.Data.AccessToken == "" || refreshEnvelope.Data.AccessToken == loginEnvelope.Data.AccessToken {
		t.Fatal("refresh must rotate the access token")
	}
	rotatedCookie := responseCookie(t, refreshResponse.Result(), keyPortalRefreshCookie)
	if rotatedCookie.Value == refreshCookie.Value {
		t.Fatal("refresh must rotate the refresh credential")
	}

	refreshAsAccessRequest := httptest.NewRequest(http.MethodGet, "/key-portal/info", nil)
	refreshAsAccessRequest.Header.Set("Authorization", "Bearer "+rotatedCookie.Value)
	refreshAsAccessResponse := httptest.NewRecorder()
	router.ServeHTTP(refreshAsAccessResponse, refreshAsAccessRequest)
	if refreshAsAccessResponse.Code != http.StatusUnauthorized {
		t.Fatalf("refresh credential used as access token returned %d", refreshAsAccessResponse.Code)
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/auth/key-logout", nil)
	logoutRequest.AddCookie(rotatedCookie)
	logoutResponse := httptest.NewRecorder()
	router.ServeHTTP(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout returned %d", logoutResponse.Code)
	}
	if cleared := responseCookie(t, logoutResponse.Result(), keyPortalRefreshCookie); cleared.MaxAge >= 0 {
		t.Fatalf("logout did not clear refresh cookie: %+v", cleared)
	}

	repository.key.KeyHash = "hash-v2"
	rotatedKeyRequest := httptest.NewRequest(http.MethodPost, "/auth/key-refresh", nil)
	rotatedKeyRequest.AddCookie(rotatedCookie)
	rotatedKeyResponse := httptest.NewRecorder()
	router.ServeHTTP(rotatedKeyResponse, rotatedKeyRequest)
	if rotatedKeyResponse.Code != http.StatusUnauthorized {
		t.Fatalf("reset key refresh returned %d", rotatedKeyResponse.Code)
	}
	repository.key.KeyHash = "hash-v1"

	repository.active = false
	disabledRequest := httptest.NewRequest(http.MethodPost, "/auth/key-refresh", nil)
	disabledRequest.AddCookie(rotatedCookie)
	disabledResponse := httptest.NewRecorder()
	router.ServeHTTP(disabledResponse, disabledRequest)
	if disabledResponse.Code != http.StatusUnauthorized {
		t.Fatalf("disabled key refresh returned %d", disabledResponse.Code)
	}
	if cleared := responseCookie(t, disabledResponse.Result(), keyPortalRefreshCookie); cleared.MaxAge >= 0 {
		t.Fatalf("invalid refresh cookie was not cleared: %+v", cleared)
	}
}

func responseCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response did not set cookie %q", name)
	return nil
}

func TestKeyPortalInfoRefreshesPeriodicUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("portal-test-signing-secret")
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"api_key_id": 134, "role": keyPortalRole, "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	NewKeyPortalHandler(userinfra.NewKeyRepository(database), secret, nil, nil, nil).RegisterRoutes(router.Group("/key-portal"))
	// A refresh must read newly settled usage, including for keys without quotas.
	for _, used := range []int64{506476, 507476} {
		amount := float64(used) / 1000000
		for range 2 {
			mock.ExpectQuery("FROM `api_keys` WHERE id = \\? AND status = \\?").
				WithArgs(uint64(134), "active", sqlmock.AnyArg(), 1).
				WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "status", "quota_used_tokens", "quota_used_amount"}).
					AddRow(134, 2, "active", used, amount))
			mock.ExpectQuery("SELECT status FROM `organizations` WHERE id = \\?").WithArgs(uint64(2)).
				WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
		}
		mock.ExpectQuery(`SELECT api_key_id, .*FROM \(SELECT .*usage_daily_rollups FORCE INDEX .*UNION ALL SELECT .*usage_logs_v2 FORCE INDEX .*UNION ALL SELECT .*usage_logs FORCE INDEX`).
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(134), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(134), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(134), sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "daily_tokens", "daily_amount", "weekly_tokens", "weekly_amount", "monthly_tokens", "monthly_amount"}).
				AddRow(134, used, amount, used, amount, used, amount))
		request := httptest.NewRequest(http.MethodGet, "/key-portal/info", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data userdomain.SafeAPIKey `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		key := envelope.Data
		if key.ID != 134 || key.DailyUsedTokens != used || key.WeeklyUsedTokens != used || key.MonthlyUsedTokens != used || key.QuotaUsedTokens != used ||
			key.DailyUsedAmount != amount || key.WeeklyUsedAmount != amount || key.MonthlyUsedAmount != amount || key.QuotaUsedAmount != amount {
			t.Fatalf("portal info contains stale or missing usage: %+v", key)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
