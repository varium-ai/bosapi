package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestGatewayModelsUseOnlyConfiguredModels(t *testing.T) {
	for _, providerType := range []string{"api_key", "oauth", "unconfigured"} {
		for _, configured := range [][]string{nil, {"", "*", "zai-org/GLM-5.3-Flash", "zai-org/GLM-5.3-Flash"}} {
			t.Run(providerType, func(t *testing.T) {
				a := testApp(t)
				a.data.Providers = []provider{{ID: 1, Type: providerType, Status: "active", Models: configured}}
				a.data.Keys = []apiKey{{ID: 1, Hash: digest("sk-gw-models"), ProviderID: 1, Status: "active"}}
				for _, path := range []string{"/v1/models", "/models"} {
					request := httptest.NewRequest(http.MethodGet, path, nil)
					request.Header.Set("Authorization", "Bearer sk-gw-models")
					response := httptest.NewRecorder()
					a.routes().ServeHTTP(response, request)
					assertModelList(t, response, len(configured) > 0)
				}
			})
		}
	}
}

func TestGatewayModelsUseAuthenticatedOrganization(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: database, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	a := testApp(t)
	a.database = db
	// The default inference route need not be one of the tenant's allowed channels.
	p := provider{ID: 9, Type: "api_key", Status: "active", Models: []string{"gpt-4o"}}
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request = withGatewayProvider(request, p)
	request = request.WithContext(context.WithValue(request.Context(), gatewayAPIKeyRequestKey{}, apiKey{ID: 5, OrgID: 2, Status: "active"}))
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set(gatewayProviderContext, p)
		ctx.Set(gatewayAPIKeyContext, apiKey{ID: 5, OrgID: 2, Status: "active"})
		ctx.Next()
	}, a.gatewayModelMiddleware())
	router.GET("/v1/models", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })

	mock.ExpectQuery("SELECT DISTINCT .*o.id = \\?").
		WithArgs(2, "active", 2, "active", "active", "active", true).
		WillReturnRows(sqlmock.NewRows([]string{"model_name", "platform"}).AddRow("zai-org/GLM-5.3-Flash", "openai"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assertModelList(t, response, true)

	mock.ExpectQuery("SELECT DISTINCT .*o.id = \\?").
		WithArgs(2, "active", 2, "active", "active", "active", true).
		WillReturnError(errors.New("database unavailable"))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("query failure returned a fallback catalog: %d %s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayModelDetailUsesAvailableCatalog(t *testing.T) {
	a := testApp(t)
	a.data.Providers = []provider{{ID: 1, Type: "api_key", Status: "active", Models: []string{"zai-org/GLM-5.3-Flash"}}}
	a.data.Keys = []apiKey{{ID: 1, Hash: digest("sk-gw-models"), ProviderID: 1, Status: "active"}}
	for _, test := range []struct {
		model  string
		status int
	}{{"zai-org/GLM-5.3-Flash", http.StatusOK}, {"gpt-4o", http.StatusNotFound}} {
		request := httptest.NewRequest(http.MethodGet, "/v1/models/"+test.model, nil)
		request.Header.Set("Authorization", "Bearer sk-gw-models")
		response := httptest.NewRecorder()
		a.routes().ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("model=%s status=%d body=%s", test.model, response.Code, response.Body.String())
		}
	}
}

func TestGatewayModelsExposeBothBoundChannels(t *testing.T) {
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
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request = request.WithContext(context.WithValue(request.Context(), gatewayAPIKeyRequestKey{}, apiKey{ID: 181, OrgID: 1, Status: "active"}))
	mock.ExpectQuery("SELECT DISTINCT .*o.id = \\?").WithArgs(1, "active", 1, "active", "active", "active", true).
		WillReturnRows(sqlmock.NewRows([]string{"model_name", "platform"}).AddRow("deepseek-ai/DeepSeek-V4.1-Flash", "openai").AddRow("zai-org/GLM-5.3-Flash", "openai"))
	response := httptest.NewRecorder()
	a.models(response, request, provider{ID: 1, Models: []string{"zai-org/GLM-5.3-Flash"}})
	var body struct {
		Data   []struct{ ID string }
		Models []struct{ ID string }
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Data) != 2 || len(body.Models) != 2 {
		t.Fatalf("bound channel lost from catalog: %d %s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func assertModelList(t *testing.T, response *httptest.ResponseRecorder, wantModel bool) {
	t.Helper()
	var list struct {
		Data   []struct{ ID string } `json:"data"`
		Models []struct{ ID string } `json:"models"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &list) != nil {
		t.Fatalf("invalid model response: %d %s", response.Code, response.Body.String())
	}
	if list.Data == nil || list.Models == nil {
		t.Fatal("model arrays must never be null")
	}
	wantCount := 0
	if wantModel {
		wantCount = 1
	}
	if len(list.Data) != wantCount || len(list.Models) != wantCount {
		t.Fatalf("unexpected models: %s", response.Body.String())
	}
	if wantModel && (list.Data[0].ID != "zai-org/GLM-5.3-Flash" || list.Models[0].ID != list.Data[0].ID) {
		t.Fatalf("unavailable model leaked: %s", response.Body.String())
	}
}

func TestGatewayModelMappingRewriteAndRestore(t *testing.T) {
	mapping := map[string]string{
		"glm-4": "zai-org/GLM-5.3-Flash",
	}
	p := provider{
		ID:           1,
		Type:         "api_key",
		Status:       "active",
		Models:       []string{"zai-org/GLM-5.3-Flash"},
		ModelMapping: mapping,
	}

	// 1. Check providerSupportsModel
	if !providerSupportsModel(p, "glm-4") {
		t.Fatal("expected provider to support mapped model glm-4")
	}
	if !providerSupportsModel(p, "zai-org/GLM-5.3-Flash") {
		t.Fatal("expected provider to support upstream model zai-org/GLM-5.3-Flash")
	}
	if providerSupportsModel(p, "non-existent") {
		t.Fatal("expected provider to NOT support non-existent model")
	}

	// 2. Check rewriteModelInJSON
	clientReq := []byte(`{"model":"glm-4","messages":[{"role":"user","content":"hi"}]}`)
	rewritten := rewriteModelInJSON(clientReq, mapping)
	var reqDoc map[string]any
	if err := json.Unmarshal(rewritten, &reqDoc); err != nil {
		t.Fatalf("failed unmarshaling rewritten json: %v", err)
	}
	if reqDoc["model"] != "zai-org/GLM-5.3-Flash" {
		t.Fatalf("expected rewritten model to be zai-org/GLM-5.3-Flash, got %v", reqDoc["model"])
	}

	// 3. Check restoreResponseModelJSON
	upstreamResp := []byte(`{"id":"chatcmpl-1","model":"zai-org/GLM-5.3-Flash","choices":[]}`)
	restored := restoreResponseModelJSON(upstreamResp, "glm-4")
	var respDoc map[string]any
	if err := json.Unmarshal(restored, &respDoc); err != nil {
		t.Fatalf("failed unmarshaling restored json: %v", err)
	}
	if respDoc["model"] != "glm-4" {
		t.Fatalf("expected restored model to be glm-4, got %v", respDoc["model"])
	}

	// 4. Check providerCost fallback
	pWithPricing := provider{
		ModelMapping: mapping,
		Pricing: map[string]any{
			"models": map[string]any{
				"zai-org/GLM-5.3-Flash": map[string]any{
					"input":  0.5,
					"output": 1.0,
				},
			},
		},
	}
	cost := providerCost(pWithPricing, "glm-4", usageMetrics{InputTokens: 1000000, OutputTokens: 1000000})
	if cost != 1.5 {
		t.Fatalf("expected cost 1.5, got %f", cost)
	}
}
