package interfaces

import (
	"context"
	"net/http"

	gatewayapp "bosapi/backend/internal/gateway/application"
)

type requestBodyLimitKey struct{}

func (a *app) requestBodyLimit() int64 {
	if a.maxRequestBodyBytes > 0 {
		return a.maxRequestBodyBytes
	}
	return gatewayapp.DefaultMaxRequestBodyBytes
}

func requestBodyLimit(r *http.Request) int64 {
	if limit, ok := r.Context().Value(requestBodyLimitKey{}).(int64); ok && limit > 0 {
		return limit
	}
	return gatewayapp.DefaultMaxRequestBodyBytes
}

func withRequestBodyLimit(r *http.Request, limit int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestBodyLimitKey{}, limit))
}
