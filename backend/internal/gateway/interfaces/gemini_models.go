package interfaces

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/upstreamurl"
)

// Discovery exposes only the authorized Gemini catalog. Fetch native metadata
// rather than inventing token limits or generation methods from model names.
func (a *app) geminiModels(w http.ResponseWriter, r *http.Request, p provider, id string) {
	key, _ := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	ids := []string{}
	if a.database != nil {
		models, err := gatewayinfra.NewModelCatalogRepository(a.database).ListAvailableModelsForKey(r.Context(), uint64(key.OrgID), key.AllowedProviderIDs, key.AllowedModels, "gemini")
		if err != nil {
			nativeError(w, "gemini", 500, errorcode.InternalError)
			return
		}
		for _, model := range models {
			ids = append(ids, model.ID)
		}
	} else if p.APIFormat == "gemini" {
		ids = append(ids, p.Models...)
		for alias := range p.ModelMapping {
			if providerSupportsModel(normalizeGeminiProvider(p), alias) {
				ids = append(ids, alias)
			}
		}
	}
	rawCount, prefixedCount := len(ids), 0
	for _, name := range ids {
		if strings.HasPrefix(name, "models/") {
			prefixedCount++
		}
	}
	for index := range ids {
		ids[index] = upstreamurl.ModelID("gemini", ids[index])
	}
	sort.Strings(ids)
	unique := ids[:0]
	for _, model := range ids {
		if validGeminiModel(model) && (len(unique) == 0 || unique[len(unique)-1] != model) {
			unique = append(unique, model)
		}
	}
	ids = unique
	usableCount := len(ids)
	if id != "" {
		if !validGeminiModel(id) {
			nativeError(w, "gemini", 404, errorcode.NotFound)
			return
		}
		found := false
		for _, model := range ids {
			if model == id {
				found = true
				break
			}
		}
		if a.database == nil && p.APIFormat == "gemini" && (len(p.Models) == 0 || containsConfiguredModel(p.Models, "*")) {
			found = true
		}
		if !found {
			nativeError(w, "gemini", 404, errorcode.NotFound)
			return
		}
		ids = []string{id}
	}
	size, offset := 50, 0
	if id == "" {
		if raw := r.URL.Query().Get("pageSize"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 1000 {
				nativeError(w, "gemini", 400, errorcode.InvalidRequest)
				return
			}
			size = n
		}
		if raw := r.URL.Query().Get("pageToken"); raw != "" {
			decoded, err := base64.RawURLEncoding.DecodeString(raw)
			if err != nil {
				nativeError(w, "gemini", 400, errorcode.InvalidRequest)
				return
			}
			n, err := strconv.Atoi(string(decoded))
			if err != nil || n < 0 || n > len(ids) {
				nativeError(w, "gemini", 400, errorcode.InvalidRequest)
				return
			}
			offset = n
		}
	}
	end := offset + size
	if end > len(ids) {
		end = len(ids)
	}
	items := []map[string]any{}
	for _, public := range ids[offset:end] {
		selected := normalizeGeminiProvider(p)
		if a.database != nil {
			var ok bool
			selected, ok = a.providerForKeyModelFormat(r.Context(), key, public, "gemini")
			if !ok {
				nativeError(w, "gemini", 503, errorcode.ProviderUnavailable)
				return
			}
		}
		actual := public
		if target := selected.ModelMapping[public]; target != "" {
			actual = target
		}
		if !validGeminiModel(actual) {
			nativeError(w, "gemini", 502, errorcode.InvalidProvider)
			return
		}
		if selected.Type == "oauth" {
			methods := []string{"generateContent", "streamGenerateContent"}
			if selected.Platform != "antigravity" {
				methods = append(methods, "countTokens")
			}
			items = append(items, map[string]any{"name": "models/" + public, "displayName": public, "supportedGenerationMethods": methods})
			continue
		}
		clone := r.Clone(r.Context())
		url := *r.URL
		url.RawQuery = ""
		clone.URL = &url
		res, cancel, err := a.nativeRoundTrip(clone, selected, "models/"+actual, nil)
		if err != nil {
			nativeError(w, "gemini", upstreamErrorStatus(r.Context(), err), upstreamErrorCode(r.Context(), err))
			return
		}
		if res.StatusCode == http.StatusTooManyRequests {
			writeUpstreamLimitError(&nativeLimitWriter{ResponseWriter: w, format: "gemini"}, res, time.Now())
			res.Body.Close()
			cancel()
			return
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
		res.Body.Close()
		cancel()
		if res.StatusCode >= 400 {
			nativeError(w, "gemini", upstreamStatus(res.StatusCode), upstreamCode(res.StatusCode))
			return
		}
		var model map[string]any
		if readErr != nil || len(raw) > 2<<20 || json.Unmarshal(raw, &model) != nil || model == nil || model["name"] != "models/"+actual {
			nativeError(w, "gemini", 502, errorcode.UpstreamError)
			return
		}
		model["name"] = "models/" + public
		if methods, ok := model["supportedGenerationMethods"].([]any); ok {
			supported := []any{}
			for _, method := range methods {
				if name, ok := method.(string); ok && geminiOperationSupported(name) {
					supported = append(supported, name)
				}
			}
			model["supportedGenerationMethods"] = supported
		}
		items = append(items, model)
	}
	generationCount := 0
	for _, model := range items {
		if methods, ok := model["supportedGenerationMethods"].([]any); ok {
			for _, method := range methods {
				if method == "generateContent" {
					generationCount++
					break
				}
			}
		}
	}
	operation := "list"
	if id != "" {
		operation = "get"
	}
	logging.Printf(r.Context(), "INFO", "gateway.models.response",
		"format=gemini operation=%s data_field=false models_field=true raw_catalog_count=%d usable_catalog_count=%d prefixed_model_names=%d models_count=%d generation_models_count=%d has_next_page=%t",
		operation, rawCount, usableCount, prefixedCount, len(items), generationCount, id == "" && end < len(ids))
	if id != "" {
		respond(w, 200, items[0])
		return
	}
	result := map[string]any{"models": items}
	if end < len(ids) {
		result["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	respond(w, 200, result)
}
