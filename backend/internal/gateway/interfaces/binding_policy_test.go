package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	admininfra "bosapi/backend/internal/admin/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestGatewayBindingsOverrideStaleOrganizationPolicy(t *testing.T) {
	for _, test := range []struct {
		name             string
		count            int
		providerID       int
		binding, model   string
		missing, failure bool
		want             errorcode.Code
		wantDiagnostic   string
	}{
		{name: "bound_glm_ignores_old_deepseek_allowlist", count: 2, binding: `[]`, model: "glm"},
		{name: "binding_allows_exact_model", count: 2, binding: `["glm"]`, model: "glm"},
		{name: "binding_restricts_model", count: 2, binding: `["deepseek"]`, model: "glm", want: errorcode.ModelNotAllowed},
		{name: "binding_wildcard_is_not_an_exact_model", count: 2, binding: `["*"]`, model: "glm", want: errorcode.ModelNotAllowed},
		{name: "unbound_or_paused_channel_denied", count: 2, missing: true, want: errorcode.InvalidProvider, wantDiagnostic: "selected provider has no active binding"},
		{name: "all_bindings_paused_does_not_restore_legacy_access", count: 1, providerID: 2, missing: true, model: "deepseek", want: errorcode.InvalidProvider},
		{name: "no_saved_bindings_uses_legacy_policy", count: 0, model: "glm", want: errorcode.InvalidProvider, wantDiagnostic: "selected provider has no active binding"},
		{name: "no_saved_bindings_denies_legacy_channel", count: 0, providerID: 2, model: "deepseek", want: errorcode.InvalidProvider, wantDiagnostic: "selected provider has no active binding"},
		{name: "binding_query_error_does_not_bypass_policy", failure: true, want: errorcode.PolicyNotAllowed, wantDiagnostic: "organization provider binding lookup failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			providerID := test.providerID
			if providerID == 0 {
				providerID = 1
			}
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			a := testApp(t)
			a.database = db
			mock.ExpectQuery("SELECT status, allowed_provider_ids, allowed_models FROM `organizations`").WithArgs(1, 1).
				WillReturnRows(sqlmock.NewRows([]string{"status", "allowed_provider_ids", "allowed_models"}).AddRow("active", `[2]`, `["deepseek"]`))
			count := mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organization_provider_bindings`").WithArgs(1)
			if test.failure {
				count.WillReturnError(errors.New("unavailable"))
			} else {
				count.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(test.count))
			}
			if test.count > 0 {
				rows := sqlmock.NewRows([]string{"organization_id", "account_id", "model_allowlist", "status"})
				if !test.missing {
					rows.AddRow(1, providerID, test.binding, "active")
				}
				mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings`").WithArgs(1, providerID, "active", 1).WillReturnRows(rows)
			}
			ctx := context.WithValue(context.Background(), gatewayDiagnosticKey{}, &gatewayDiagnosticState{})
			key := apiKey{OrgID: 1, AllowedProviderIDs: []uint64{uint64(providerID)}}
			if test.model != "" {
				key.AllowedModels = []string{test.model}
			}
			if got := a.databasePolicyDenial(ctx, key, providerID, test.model); got != test.want {
				t.Fatalf("denial=%s want=%s", got, test.want)
			}
			if test.wantDiagnostic != "" {
				code, diagnostic := gatewayFailureDiagnostic(ctx)
				if code != test.want || !strings.Contains(diagnostic, test.wantDiagnostic) {
					t.Fatalf("code=%s diagnostic=%s", code, diagnostic)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBoundModelsRouteToTheirOwnChannelsDespiteStaleOrganizationPolicy(t *testing.T) {
	for _, target := range []struct {
		model    string
		id       int
		wantPath string
	}{
		{"glm", 1, "/api-llm/glm5/v1/chat/completions"},
		{"deepseek", 2, "/channel2/v1/chat/completions"},
	} {
		t.Run(target.model, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			a := testApp(t)
			a.database = db
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Model string `json:"model"`
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Model != target.model || r.URL.Path != target.wantPath {
					t.Errorf("wrong upstream or body: path=%s model=%s", r.URL.Path, payload.Model)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[]}`)
			}))
			defer upstream.Close()
			a.client = upstream.Client()
			cipher, err := admininfra.NewCredentialCipher(a.encryptionKey)
			if err != nil {
				t.Fatal(err)
			}
			credentials, err := cipher.Encrypt("test-upstream-key")
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectQuery("SELECT \\* FROM `accounts`").WithArgs("active", true, 1, "active").
				WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform", "api_format", "base_url", "credentials_ciphertext"}).
					AddRow(1, "GLM", "openai", "openai_chat", upstream.URL+"/api-llm/glm5", []byte(credentials)).
					AddRow(2, "DeepSeek", "openai", "openai_chat", upstream.URL+"/channel2/v1", []byte(credentials)))
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organization_provider_bindings`").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
			mock.ExpectQuery("SELECT account_id, priority, model_allowlist FROM `organization_provider_bindings`").WithArgs(1, "active").
				WillReturnRows(sqlmock.NewRows([]string{"account_id", "priority", "model_allowlist"}).
					AddRow(2, 4, `[]`).AddRow(1, 3, `[]`))
			for _, id := range []int{2, 1} {
				model := "deepseek"
				if id == 1 {
					model = "glm"
				}
				mock.ExpectQuery("SELECT model_name, price_input_per_1m, price_output_per_1m, price_cache_read_per_1m, price_cache_write_per_1m FROM `provider_models`").WithArgs(id, "active").
					WillReturnRows(sqlmock.NewRows([]string{"model_name", "price_input_per_1m", "price_output_per_1m"}).AddRow(model, 0, 0))
				if model == target.model {
					break
				}
			}
			expectPolicy := func() {
				mock.ExpectQuery("SELECT status, allowed_provider_ids, allowed_models FROM `organizations`").WithArgs(1, 1).
					WillReturnRows(sqlmock.NewRows([]string{"status", "allowed_provider_ids", "allowed_models"}).AddRow("active", `[2]`, `["deepseek"]`))
				mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organization_provider_bindings`").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
				mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings`").WithArgs(1, target.id, "active", 1).
					WillReturnRows(sqlmock.NewRows([]string{"organization_id", "account_id", "model_allowlist", "status"}).AddRow(1, target.id, `[]`, "active"))
			}
			expectPolicy()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"`+target.model+`","messages":[{"role":"user","content":"hello"}]}`))
			// The initial inference route does not serve the requested model.
			// Model middleware must select the matching bound channel before proxying.
			p := provider{ID: 9, Type: "api_key", Models: []string{"other"}}
			expectPolicy()
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(gatewayProviderContext, p)
				c.Set(gatewayAPIKeyContext, apiKey{OrgID: 1})
				c.Request = withGatewayProvider(c.Request, p)
				c.Next()
			}, a.gatewayModelMiddleware())
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				selected, _ := c.Get(gatewayProviderContext)
				if selected.(provider).ID != target.id {
					t.Errorf("wrong selected provider: %+v", selected)
				}
				a.gateway(c.Writer, c.Request)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProviderSelectionHonorsBindingPriorityAndPausedPolicy(t *testing.T) {
	for _, paused := range []bool{false, true} {
		name := "binding_priority_overrides_provider_priority"
		if paused {
			name = "all_paused_does_not_select_a_legacy_channel"
		}
		t.Run(name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			a := testApp(t)
			a.database = db
			cipher, err := admininfra.NewCredentialCipher(a.encryptionKey)
			if err != nil {
				t.Fatal(err)
			}
			credentials, err := cipher.Encrypt("test-key")
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectQuery("SELECT \\* FROM `accounts`").WithArgs("active", true, 1, "active").
				WillReturnRows(sqlmock.NewRows([]string{"id", "priority", "credentials_ciphertext"}).AddRow(1, 100, []byte(credentials)).AddRow(2, 0, []byte(credentials)))
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organization_provider_bindings`").WithArgs(1).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
			bindings := sqlmock.NewRows([]string{"account_id", "priority", "model_allowlist"})
			if !paused {
				bindings.AddRow(2, 10, `[]`).AddRow(1, 0, `[]`)
			}
			mock.ExpectQuery("SELECT account_id, priority, model_allowlist FROM `organization_provider_bindings`").WithArgs(1, "active").WillReturnRows(bindings)
			if !paused {
				mock.ExpectQuery("SELECT model_name, price_input_per_1m, price_output_per_1m, price_cache_read_per_1m, price_cache_write_per_1m FROM `provider_models`").WithArgs(2, "active").
					WillReturnRows(sqlmock.NewRows([]string{"model_name", "price_input_per_1m", "price_output_per_1m"}).AddRow("shared-model", 0, 0))
				mock.ExpectQuery("SELECT status, allowed_provider_ids, allowed_models FROM `organizations`").WithArgs(1, 1).
					WillReturnRows(sqlmock.NewRows([]string{"status", "allowed_provider_ids", "allowed_models"}).AddRow("active", `[1]`, `["old-model"]`))
				mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organization_provider_bindings`").WithArgs(1).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
				mock.ExpectQuery("SELECT \\* FROM `organization_provider_bindings`").WithArgs(1, 2, "active", 1).
					WillReturnRows(sqlmock.NewRows([]string{"organization_id", "account_id", "status", "model_allowlist"}).AddRow(1, 2, "active", `[]`))
			}
			selected, ok := a.providerForKeyModel(context.Background(), apiKey{OrgID: 1}, "shared-model")
			if paused {
				if ok || selected.ID != 0 {
					t.Fatalf("paused policy selected a legacy channel: %+v", selected)
				}
			} else if !ok || selected.ID != 2 {
				t.Fatalf("binding priority was ignored: selected=%+v ok=%v", selected, ok)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
