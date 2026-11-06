package interfaces

import (
	"context"
	"net/http"
	"strings"

	"bosapi/backend/internal/platform/errorcode"
	"github.com/gin-gonic/gin"
)

type geminiRequestContextKey struct{}
type geminiRequest struct{ Model, Operation string }

// Agents may append /v1beta for discovery, but send inference directly under
// an OpenAI-style /v1 base. Recognize native POST model actions without changing
// the OpenAI GET /v1/models catalog or redirecting credential-bearing requests.
func geminiNativePath(r *http.Request) string {
	path := r.URL.Path
	if strings.HasPrefix(path, "/v1/v1beta/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	if r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/models/") && strings.Contains(path, ":") {
		path = "/v1beta/" + strings.TrimPrefix(path, "/v1/")
	}
	if path == "/v1beta/models" || strings.HasPrefix(path, "/v1beta/models/") {
		return path
	}
	return ""
}

func validGeminiModel(model string) bool {
	if model == "" || len(model) > 191 || model == "." || model == ".." {
		return false
	}
	for _, char := range model {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}

func geminiModelAction(path string) (string, string, bool) {
	if !strings.HasPrefix(path, "models/") {
		return "", "", false
	}
	model, operation, ok := strings.Cut(strings.TrimPrefix(path, "models/"), ":")
	return model, operation, ok && validGeminiModel(model) && operation != "" && len(operation) <= 64 && !strings.ContainsAny(operation, "/:?#%\\ \t\r\n")
}

func geminiOperationSupported(operation string) bool {
	switch operation {
	case "generateContent", "streamGenerateContent", "countTokens", "embedContent", "batchEmbedContents":
		return true
	}
	return false
}

func requestGemini(r *http.Request) (geminiRequest, bool) {
	if metadata, ok := r.Context().Value(geminiRequestContextKey{}).(geminiRequest); ok {
		return metadata, true
	}
	path := geminiNativePath(r)
	if !strings.HasPrefix(path, "/v1beta/models/") {
		return geminiRequest{}, false
	}
	model, operation, ok := geminiModelAction(strings.TrimPrefix(path, "/v1beta/"))
	return geminiRequest{Model: model, Operation: operation}, ok
}

func requestOperation(r *http.Request) string {
	if metadata, ok := requestGemini(r); ok {
		return metadata.Operation
	}
	return ""
}

// Freeze native routing metadata before quota/security inspect or consume the
// body. Malformed URL models must never fall back to JSON/query model fields.
func (a *app) gatewayGeminiMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost || geminiNativePath(c.Request) == "" {
			c.Next()
			return
		}
		metadata, ok := requestGemini(c.Request)
		if !ok || !geminiOperationSupported(metadata.Operation) {
			recordGatewayFailure(c.Request, "request_validation", errorcode.InvalidRequest, "endpoint", "invalid Gemini URL model or unsupported operation")
			nativeError(c.Writer, "gemini", http.StatusBadRequest, errorcode.InvalidRequest)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), geminiRequestContextKey{}, metadata))
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayModelRequestKey{}, metadata.Model))
		c.Next()
	}
}
