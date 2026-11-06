package interfaces

import (
	"context"
	"net/http"

	admindomain "bosapi/backend/internal/admin/domain"
)

type gatewayToolSecurityModeKey struct{}

func (a *app) readToolSecurityPolicy(ctx context.Context) (admindomain.ToolSecurityPolicy, error) {
	if a.toolSecurityPolicy == nil {
		return admindomain.ToolSecurityPolicy{Mode: "observe"}, nil
	}
	policy, err := a.toolSecurityPolicy(ctx)
	if err != nil {
		return policy, err
	}
	return policy, policy.Validate()
}

// Capture one mode per request/connection so a live stream has consistent behavior.
func (a *app) toolSecurityBlocks(r *http.Request) bool {
	if mode, ok := r.Context().Value(gatewayToolSecurityModeKey{}).(string); ok {
		return mode == "block"
	}
	policy, err := a.readToolSecurityPolicy(r.Context())
	return err != nil || policy.Mode == "block"
}
