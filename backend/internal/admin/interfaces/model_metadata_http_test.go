package interfaces

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"github.com/gin-gonic/gin"
)

type modelMetadataPagingRepository struct {
	domain.ModelMetadataRepository
	page, size int
	calls      int
}

func (repository *modelMetadataPagingRepository) List(_ context.Context, _ string, page, size int) ([]domain.ModelMetadata, int64, error) {
	repository.page, repository.size = page, size
	repository.calls++
	return []domain.ModelMetadata{}, 0, nil
}

func TestModelMetadataPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		query              string
		status, page, size int
	}{
		{status: http.StatusOK, page: 1, size: 20},
		{query: "page=2&page_size=100", status: http.StatusOK, page: 2, size: 100},
		{query: "limit=5", status: http.StatusOK, page: 1, size: 5},
		{query: "page_size=7&limit=invalid", status: http.StatusOK, page: 1, size: 7},
		{query: "page=abc", status: http.StatusBadRequest},
		{query: "page=0", status: http.StatusBadRequest},
		{query: "page_size=abc", status: http.StatusBadRequest},
		{query: "page_size=0", status: http.StatusBadRequest},
		{query: "page_size=101", status: http.StatusBadRequest},
		{query: "limit=101", status: http.StatusBadRequest},
	} {
		t.Run(test.query, func(t *testing.T) {
			repository := &modelMetadataPagingRepository{}
			router := gin.New()
			NewModelMetadataHandler(application.NewModelMetadataService(repository)).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/model-metadata?"+test.query, nil))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status == http.StatusBadRequest {
				if repository.calls != 0 {
					t.Fatal("invalid pagination reached repository")
				}
				return
			}
			var payload struct {
				Data struct {
					Page int `json:"page"`
					Size int `json:"page_size"`
				}
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if repository.calls != 1 || repository.page != test.page || repository.size != test.size || payload.Data.Page != test.page || payload.Data.Size != test.size {
				t.Fatalf("repository=%+v response=%+v", repository, payload)
			}
		})
	}
}
