package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"

	"bosapi/backend/internal/platform/errorcode"
	"github.com/gin-gonic/gin"
)

type requestIDContextKey struct{}
type logMetadataContextKey struct{}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// LogMetadata contains safe request identity fields shared by application logs.
type LogMetadata struct {
	RequestID      string
	ClientIP       string
	OrganizationID uint64
	APIKeyMasked   string
}

// Claims contains the identity and organization scope authenticated from a portal JWT.
type Claims struct {
	UserID         uint64 `json:"sub"`
	OrganizationID uint64 `json:"organization_id"`
	Role           string `json:"role"`
	Email          string `json:"email"`
	JTI            string `json:"jti"`
}

// SuccessResponse wraps portal results with a request identifier.
type SuccessResponse struct {
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
}

// ErrorResponse keeps failures stable and avoids leaking persistence or provider details.
type ErrorResponse struct {
	Error     Error  `json:"error"`
	RequestID string `json:"request_id"`
}

// Error describes a safe API error code and message.
type Error struct {
	Code    errorcode.Code `json:"code"`
	Message string         `json:"message"`
	Details any            `json:"details"`
}

// Middleware adds request identifiers, security headers, and a bounded request body.
func Middleware(maxBodyBytes int64) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		requestID := strings.TrimSpace(ctx.GetHeader("X-Request-ID"))
		if !requestIDPattern.MatchString(requestID) {
			requestID = newRequestID()
		}
		ctx.Set("request_id", requestID)
		requestContext := WithRequestID(ctx.Request.Context(), requestID)
		requestContext = WithLogMetadata(requestContext, LogMetadata{RequestID: requestID, ClientIP: ctx.ClientIP()})
		ctx.Request = ctx.Request.WithContext(requestContext)
		ctx.Header("X-Request-ID", requestID)
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("X-Frame-Options", "DENY")
		ctx.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		ctx.Header("Referrer-Policy", "no-referrer")
		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxBodyBytes)
		ctx.Next()
	}
}

// WithRequestID attaches an HTTP request identifier to a context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

// RequestIDFromContext returns the request identifier assigned by Middleware.
func RequestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey{}).(string)
	return value
}

// WithLogMetadata returns a context with the supplied non-empty metadata fields merged in.
func WithLogMetadata(ctx context.Context, metadata LogMetadata) context.Context {
	current, _ := ctx.Value(logMetadataContextKey{}).(LogMetadata)
	if metadata.RequestID != "" {
		current.RequestID = metadata.RequestID
	}
	if metadata.ClientIP != "" {
		current.ClientIP = metadata.ClientIP
	}
	if metadata.OrganizationID != 0 {
		current.OrganizationID = metadata.OrganizationID
	}
	if metadata.APIKeyMasked != "" {
		current.APIKeyMasked = metadata.APIKeyMasked
	}
	return context.WithValue(ctx, logMetadataContextKey{}, current)
}

// LogMetadataFromContext returns safe request identity fields, if present.
func LogMetadataFromContext(ctx context.Context) LogMetadata {
	metadata, _ := ctx.Value(logMetadataContextKey{}).(LogMetadata)
	if metadata.RequestID == "" {
		metadata.RequestID = RequestIDFromContext(ctx)
	}
	return metadata
}

// MaskAPIKey returns a short, non-reversible display value for a key prefix or credential.
func MaskAPIKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) > 4 {
		return string(runes[:4]) + "••••••"
	}
	return "••••••"
}

// ValidRequestID reports whether value uses the safe request ID character set.
func ValidRequestID(value string) bool { return requestIDPattern.MatchString(value) }

// WriteSuccess writes a success envelope using the current request ID.
func WriteSuccess(ctx *gin.Context, status int, data any) {
	ctx.JSON(status, SuccessResponse{Data: data, RequestID: requestID(ctx)})
}

// WriteError writes a sanitized error envelope and aborts the current route chain.
func WriteError(ctx *gin.Context, status int, code errorcode.Code, message string) {
	ctx.AbortWithStatusJSON(status, ErrorResponse{Error: Error{Code: code, Message: message, Details: struct{}{}}, RequestID: requestID(ctx)})
}

// CurrentClaims returns typed JWT claims from the request context.
func CurrentClaims(ctx *gin.Context) (Claims, bool) {
	value, exists := ctx.Get("identity")
	if !exists {
		return Claims{}, false
	}
	claims, ok := value.(Claims)
	return claims, ok
}

func requestID(ctx *gin.Context) string {
	value, _ := ctx.Get("request_id")
	requestID, _ := value.(string)
	return requestID
}

func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "req_unavailable"
	}
	return "req_" + hex.EncodeToString(value[:])
}
