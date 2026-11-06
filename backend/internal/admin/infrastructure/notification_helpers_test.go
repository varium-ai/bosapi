package infrastructure

import (
	"bosapi/backend/internal/admin/domain"
	"context"
	"net/http"
)

type reportNotificationSettings struct {
	value string
	err   error
}

func (settings reportNotificationSettings) Get(context.Context, string) (*domain.SystemSetting, error) {
	if settings.value == "missing" {
		return nil, settings.err
	}
	return &domain.SystemSetting{ValueText: settings.value}, settings.err
}
func (reportNotificationSettings) Set(context.Context, string, string, bool, *uint64) error {
	return nil
}
func (reportNotificationSettings) Delete(context.Context, string) error { return nil }
func (reportNotificationSettings) List(context.Context, string) ([]domain.SystemSetting, error) {
	return nil, nil
}

type reportHTTPTransport func(*http.Request) (*http.Response, error)

func (f reportHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
