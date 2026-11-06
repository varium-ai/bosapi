package interfaces

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	userdomain "bosapi/backend/internal/user/domain"
	"github.com/gin-gonic/gin"
)

type adminKeyServiceStub struct {
	imported       []application.AdminKeyInput
	created        application.AdminKeyInput
	createCalls    int
	deletedIDs     []uint64
	deletedOrg     uint64
	revealCalls    int
	duplicateCalls int
	duplicateOrg   uint64
	updated        map[string]any
	updatedOrg     uint64
}

func (stub *adminKeyServiceStub) List(context.Context, domain.AdminKeyFilter, int, int) ([]userdomain.SafeAPIKey, int64, error) {
	return nil, 0, nil
}
func (stub *adminKeyServiceStub) SetStatus(context.Context, uint64, uint64, string) error { return nil }
func (stub *adminKeyServiceStub) Update(_ context.Context, _ uint64, organizationID uint64, updates map[string]any) error {
	stub.updated = updates
	stub.updatedOrg = organizationID
	return nil
}
func (stub *adminKeyServiceStub) Create(_ context.Context, input application.AdminKeyInput) (application.CreatedAdminKey, error) {
	stub.created = input
	stub.createCalls++
	return application.CreatedAdminKey{}, nil
}
func (stub *adminKeyServiceStub) CreateBatch(_ context.Context, inputs []application.AdminKeyInput) ([]application.CreatedAdminKey, error) {
	stub.imported = inputs
	return []application.CreatedAdminKey{{Name: inputs[0].Name, Key: "new-fixture-secret"}}, nil
}
func (stub *adminKeyServiceStub) Import(_ context.Context, inputs []application.AdminKeyInput) ([]application.CreatedAdminKey, error) {
	stub.imported = inputs
	created := make([]application.CreatedAdminKey, len(inputs))
	for index := range inputs {
		created[index] = application.CreatedAdminKey{ID: uint64(index + 1), KeyPrefix: "safe-prefix", MaskedKey: "safe-prefix••••"}
	}
	return created, nil
}
func (stub *adminKeyServiceStub) Reset(context.Context, uint64, uint64) (application.CreatedAdminKey, error) {
	return application.CreatedAdminKey{}, nil
}

func (stub *adminKeyServiceStub) Duplicate(_ context.Context, _ uint64, organizationID uint64) (application.CreatedAdminKey, error) {
	stub.duplicateCalls++
	stub.duplicateOrg = organizationID
	return application.CreatedAdminKey{}, nil
}
func (stub *adminKeyServiceStub) Get(context.Context, uint64, uint64) (userdomain.SafeAPIKey, error) {
	return userdomain.SafeAPIKey{}, nil
}
func (stub *adminKeyServiceStub) BatchDelete(_ context.Context, ids []uint64, organizationID uint64) (int64, error) {
	stub.deletedIDs = ids
	stub.deletedOrg = organizationID
	return int64(len(ids)), nil
}
func (stub *adminKeyServiceStub) Reveal(context.Context, uint64, uint64, uint64, bool, string) (string, error) {
	stub.revealCalls++
	return "", nil
}

func TestAdminKeyRevealRequiresOTPAndDoesNotCacheSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		role, body    string
		status, calls int
	}{
		{"super_admin", "", 400, 0},
		{"super_admin", `{}`, 400, 0},
		{"org_admin", `{"code":"123"}`, 400, 0},
		{"super_admin", `{"code":"123456"}`, 200, 1},
		{"org_admin", `{"code":"123456"}`, 200, 1},
		{"user", `{"code":"123456"}`, 403, 0},
		{"", `{"code":"123456"}`, 401, 0},
	} {
		t.Run(test.role+test.body, func(t *testing.T) {
			service := &adminKeyServiceStub{}
			router := gin.New()
			if test.role != "" {
				router.Use(func(ctx *gin.Context) {
					ctx.Set("identity", httpapi.Claims{UserID: 19, Role: test.role, OrganizationID: 3})
				})
			}
			NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/keys/7/reveal", strings.NewReader(test.body)))
			if response.Code != test.status || service.revealCalls != test.calls || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d calls=%d headers=%v", response.Code, service.revealCalls, response.Header())
			}
		})
	}
}

func TestAdminKeyImportAcceptsAdminBatchAndDoesNotReturnRawKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &adminKeyServiceStub{}
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 19, Role: "super_admin"})
		ctx.Next()
	})
	NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
	body := `{"keys":[{"name":"production","api_key":"first-imported-secret-key-value"},{"name":"staging","api_key":"second-imported-secret-key-value"}],"user_id":4,"organization_id":9}`
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/keys/import", strings.NewReader(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(service.imported) != 2 || service.imported[0].APIKey != "first-imported-secret-key-value" || service.imported[1].OrganizationID != 9 {
		t.Fatalf("import inputs=%+v", service.imported)
	}
	if strings.Contains(response.Body.String(), "first-imported-secret-key-value") || strings.Contains(response.Body.String(), "second-imported-secret-key-value") {
		t.Fatalf("raw API key was returned: %s", response.Body.String())
	}
}

func TestAdminKeyImportEnforcesOrganizationScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &adminKeyServiceStub{}
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 19, Role: "org_admin", OrganizationID: 3})
		ctx.Next()
	})
	NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
	body := `{"keys":[{"name":"production","api_key":"first-imported-secret-key-value"}],"user_id":4,"organization_id":9}`
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/keys/import", strings.NewReader(body)))
	if response.Code != http.StatusNotFound || len(service.imported) != 0 {
		t.Fatalf("status=%d imported=%d body=%s", response.Code, len(service.imported), response.Body.String())
	}
}

func TestAdminKeyCreateAllowsOnlySuperAdminToCrossOrganizations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		role        string
		wantStatus  int
		wantCreated bool
	}{
		{role: "super_admin", wantStatus: http.StatusCreated, wantCreated: true},
		{role: "org_admin", wantStatus: http.StatusNotFound},
	} {
		t.Run(test.role, func(t *testing.T) {
			service := &adminKeyServiceStub{}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 19, Role: test.role, OrganizationID: 3})
				ctx.Next()
			})
			NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			body := `{"name":"cross-organization","organization_id":9,"allowed_provider_ids":[7],"allowed_models":["one"]}`
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/keys", strings.NewReader(body)))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantCreated {
				if service.createCalls != 1 || service.created.OrganizationID != 9 || service.created.UserID != 0 || len(service.created.AllowedProviderIDs) != 1 || len(service.created.AllowedModels) != 1 {
					t.Fatalf("create calls=%d input=%+v", service.createCalls, service.created)
				}
			} else if service.createCalls != 0 {
				t.Fatalf("ordinary administrator created a cross-organization key: %+v", service.created)
			}
		})
	}
}

func TestAdminKeyOrganizationChangeRequiresSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		role       string
		body       string
		wantStatus int
		wantOrg    uint64
	}{
		{role: "super_admin", body: `{"organization_id":9}`, wantStatus: http.StatusOK, wantOrg: 9},
		{role: "org_admin", body: `{"organization_id":9}`, wantStatus: http.StatusForbidden},
		{role: "super_admin", body: `{"user_id":4}`, wantStatus: http.StatusBadRequest},
	} {
		t.Run(test.role, func(t *testing.T) {
			service := &adminKeyServiceStub{}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 19, Role: test.role, OrganizationID: 3})
				ctx.Next()
			})
			NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/admin/keys/7", strings.NewReader(test.body)))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantOrg != 0 {
				if service.updatedOrg != 0 || service.updated["organization_id"] != test.wantOrg {
					t.Fatalf("scope=%d updates=%+v", service.updatedOrg, service.updated)
				}
			} else if service.updated != nil {
				t.Fatalf("unauthorized ownership update reached service: %+v", service.updated)
			}
		})
	}
}

func TestAdminKeyDeleteUsesPhysicalDeleteServiceWithinOrganization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &adminKeyServiceStub{}
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 19, Role: "org_admin", OrganizationID: 3})
		ctx.Next()
	})
	NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/admin/keys/7", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(service.deletedIDs) != 1 || service.deletedIDs[0] != 7 || service.deletedOrg != 3 {
		t.Fatalf("delete request ids=%v organization=%d", service.deletedIDs, service.deletedOrg)
	}
}

func TestAdminKeyDuplicateEnforcesRoleAndOrganizationScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		role          string
		status, calls int
		scope         uint64
	}{
		{"super_admin", 201, 1, 0}, {"org_admin", 201, 1, 3}, {"user", 403, 0, 0}, {"", 401, 0, 0},
	} {
		t.Run(test.role, func(t *testing.T) {
			service := &adminKeyServiceStub{}
			router := gin.New()
			if test.role != "" {
				router.Use(func(ctx *gin.Context) {
					ctx.Set("identity", httpapi.Claims{UserID: 19, Role: test.role, OrganizationID: 3})
				})
			}
			NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/keys/7/duplicate", nil))
			if response.Code != test.status || service.duplicateCalls != test.calls || service.duplicateOrg != test.scope || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected duplicate response: status=%d calls=%d scope=%d", response.Code, service.duplicateCalls, service.duplicateOrg)
			}
		})
	}
}

func TestAdminKeyUpdateExpirationSupportsSetClearAndOmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name, body string
		present    bool
		status     int
	}{
		{"set", `{"expires_at":"2030-01-01T12:30:00+08:00"}`, true, 200},
		{"clear", `{"expires_at":null}`, true, 200},
		{"omit", `{"name":"renamed"}`, false, 200},
		{"invalid", `{"expires_at":"invalid"}`, false, 400},
		{"wrong type", `{"expires_at":1}`, false, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &adminKeyServiceStub{}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 19, OrganizationID: 3, Role: "org_admin"})
			})
			NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/admin/keys/7", strings.NewReader(test.body)))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status != 200 {
				if service.updated != nil {
					t.Fatal("invalid expiration reached service")
				}
				return
			}
			value, present := service.updated["expires_at"]
			if present != test.present || service.updatedOrg != 3 {
				t.Fatal("expiration presence or scope changed")
			}
			if test.name == "clear" {
				if expiration, ok := value.(*time.Time); !ok || expiration != nil {
					t.Fatal("null must clear expiration")
				}
			}
			if test.name == "set" {
				expiration, ok := value.(*time.Time)
				if !ok || expiration == nil || expiration.Format(time.RFC3339) != "2030-01-01T12:30:00+08:00" {
					t.Fatal("expiration was not preserved")
				}
			}
		})
	}
}

func TestAdminKeyUpdateSecurityInspectionEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name              string
		body              string
		upstreamPresent   bool
		upstreamEnabled   bool
		downstreamPresent bool
		downstreamEnabled bool
	}{
		{"legacy disable", `{"security_inspection_enabled":false}`, true, false, true, false},
		{"legacy enable", `{"security_inspection_enabled":true}`, true, true, true, true},
		{"upstream disable", `{"security_inspection_upstream_enabled":false}`, true, false, false, true},
		{"downstream disable", `{"security_inspection_downstream_enabled":false}`, false, true, true, false},
		{"both disable", `{"security_inspection_upstream_enabled":false,"security_inspection_downstream_enabled":false}`, true, false, true, false},
		{"omit", `{"name":"renamed"}`, false, true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &adminKeyServiceStub{}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 19, OrganizationID: 3, Role: "org_admin"})
			})
			NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/admin/keys/7", strings.NewReader(test.body)))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			for name, test2 := range map[string]struct {
				present bool
				value   bool
			}{"upstream": {test.upstreamPresent, test.upstreamEnabled}, "downstream": {test.downstreamPresent, test.downstreamEnabled}} {
				value, present := service.updated["security_inspection_"+name+"_enabled"]
				if present != test2.present {
					t.Fatalf("%s presence changed: present=%t", name, present)
				}
				if present {
					enabled, ok := value.(bool)
					if !ok || enabled != test2.value {
						t.Fatalf("%s value=%v ok=%t", name, value, ok)
					}
				}
			}
		})
	}
}

func TestAdminKeyImportAppliesSharedSettingsToEveryCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)
	zero, amount := float64(0), float64(2)
	tokens, rate := int64(100), int64(200)
	expires := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	settings := domain.KeyTemplateSettings{OrganizationID: 3, UserID: 4, ExpiresAt: &expires, QuotaAmount: &zero, QuotaTokens: &tokens,
		DailyQuotaAmount: &amount, WeeklyQuotaAmount: &amount, MonthlyQuotaAmount: &amount,
		DailyQuotaTokens: &tokens, WeeklyQuotaTokens: &tokens, MonthlyQuotaTokens: &tokens,
		RateLimit5H: &rate, RateLimit1D: &rate, RateLimit7D: &rate, IPAllowlist: []string{"192.0.2.0/24"}, IPBlocklist: []string{"::1"}}
	body, err := json.Marshal(struct {
		Keys []struct {
			Name   string `json:"name"`
			APIKey string `json:"api_key"`
		} `json:"keys"`
		domain.KeyTemplateSettings
	}{
		Keys: []struct {
			Name   string `json:"name"`
			APIKey string `json:"api_key"`
		}{{"first", "first-complete-fixture-key"}, {"second", "second-complete-fixture-key"}}, KeyTemplateSettings: settings})
	if err != nil {
		t.Fatal(err)
	}
	service := &adminKeyServiceStub{}
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{UserID: 19, OrganizationID: 3, Role: "org_admin"})
	})
	NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/keys/import", strings.NewReader(string(body))))
	if response.Code != 201 || len(service.imported) != 2 {
		t.Fatalf("unexpected import response: %d %s", response.Code, response.Body.String())
	}
	expected := application.AdminKeyInput{OrganizationID: 3, ExpiresAt: &expires, QuotaAmount: &zero, QuotaTokens: &tokens,
		DailyQuotaAmount: &amount, WeeklyQuotaAmount: &amount, MonthlyQuotaAmount: &amount,
		DailyQuotaTokens: &tokens, WeeklyQuotaTokens: &tokens, MonthlyQuotaTokens: &tokens,
		RateLimit5H: &rate, RateLimit1D: &rate, RateLimit7D: &rate, IPAllowlist: settings.IPAllowlist, IPBlocklist: settings.IPBlocklist}
	for index, input := range service.imported {
		if index == 0 {
			expected.Name = "first"
			expected.APIKey = "first-complete-fixture-key"
		} else {
			expected.Name = "second"
			expected.APIKey = "second-complete-fixture-key"
		}
		if !reflect.DeepEqual(input, expected) {
			t.Fatalf("batch settings missing from row %d", index)
		}
	}
}

func TestAdminKeyBatchCreationEnforcesScopeAndValidatesNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		role, body string
		status     int
	}{
		{"super_admin", `{"names":["first","second"],"organization_id":3,"user_id":2,"quota_tokens":100}`, 201},
		{"org_admin", `{"names":["first","second"],"organization_id":3,"user_id":2}`, 201},
		{"org_admin", `{"names":["first"],"organization_id":4,"user_id":2}`, 404},
		{"user", `{"names":["first"],"organization_id":3,"user_id":2}`, 403},
		{"", `{"names":["first"],"organization_id":3,"user_id":2}`, 401},
		{"super_admin", `{"names":[],"organization_id":3,"user_id":2}`, 400},
		{"super_admin", `{"names":[""],"organization_id":3,"user_id":2}`, 400},
	} {
		t.Run(test.role+test.body, func(t *testing.T) {
			service := &adminKeyServiceStub{}
			router := gin.New()
			if test.role != "" {
				router.Use(func(ctx *gin.Context) {
					ctx.Set("identity", httpapi.Claims{UserID: 19, OrganizationID: 3, Role: test.role})
				})
			}
			NewAdminKeyHandler(service).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/keys/batch-create", strings.NewReader(test.body)))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status == 201 {
				if len(service.imported) != 2 || service.imported[0].Name != "first" || service.imported[1].Name != "second" || service.imported[1].OrganizationID != 3 || service.imported[1].APIKey != "" || response.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("batch mapping or secret caching failed")
				}
			} else if len(service.imported) != 0 {
				t.Fatal("invalid batch reached service")
			}
		})
	}
}
