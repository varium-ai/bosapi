package interfaces

import (
	"context"
	"net/http"

	admindomain "bosapi/backend/internal/admin/domain"
)

type gatewaySecurityInspectionModeKey struct{}

func (a *app) readSecurityInspectionPolicy(ctx context.Context) (admindomain.SecurityInspectionPolicy, error) {
	if a.securityInspectionPolicy == nil {
		return admindomain.SecurityInspectionPolicy{Mode: "observe"}, nil
	}
	policy, err := a.securityInspectionPolicy(ctx)
	if err != nil {
		return admindomain.SecurityInspectionPolicy{Mode: "observe"}, err
	}
	if err := policy.Validate(); err != nil {
		return admindomain.SecurityInspectionPolicy{Mode: "observe"}, err
	}
	return policy, nil
}

// Capture one mode per request/connection so a live stream has consistent behavior.
func withSecurityInspectionMode(r *http.Request, mode string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), gatewaySecurityInspectionModeKey{}, mode))
}

func globalSecurityInspectionDisabled(ctx context.Context) bool {
	mode, ok := ctx.Value(gatewaySecurityInspectionModeKey{}).(string)
	return ok && mode == "disabled"
}
