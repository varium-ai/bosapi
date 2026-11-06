package interfaces

import (
	"bytes"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelCatalogLogsExplainFilteredNamesWithoutExposingContent(t *testing.T) {
	for _, native := range []bool{false, true} {
		a, key := geminiTestApp(t)
		a.data.Providers[0].Models = []string{"models/models/PRIVATE_MODEL_NAME"}
		a.data.Providers[0].ModelMapping = nil
		var output bytes.Buffer
		previous := log.Writer()
		log.SetOutput(&output)
		path := "/v1/models"
		if native {
			path = "/v1/v1beta/models?key=" + key
		}
		r := httptest.NewRequest("GET", path, nil)
		if !native {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		log.SetOutput(previous)
		if w.Code != 200 {
			t.Fatalf("diagnostics changed response: %d %s", w.Code, w.Body.String())
		}
		logs := output.String()
		expected := []string{"event=gateway.models.response", "raw_catalog_count=1"}
		if native {
			expected = append(expected, "format=gemini", "usable_catalog_count=0", "prefixed_model_names=1", "models_count=0")
		} else {
			expected = append(expected, "format=openai", "models_count=1")
		}
		for _, value := range expected {
			if !strings.Contains(logs, value) {
				t.Fatalf("missing %q: %s", value, logs)
			}
		}
		for _, secret := range []string{key, "PRIVATE_MODEL_NAME", "provider-key"} {
			if strings.Contains(logs, secret) {
				t.Fatal("catalog logging exposed credential or model content")
			}
		}
	}
}
