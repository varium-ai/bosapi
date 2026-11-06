package domain

import "context"

// PublicModel contains model metadata allowed for anonymous portal discovery.
type PublicModel struct {
	ID           string         `json:"id"`
	Platform     string         `json:"platform"`
	Capabilities map[string]any `json:"capabilities"`
	Status       string         `json:"status"`
}

// PublicModelRepository reads models enabled by organization publication policy.
type PublicModelRepository interface {
	ListPublicModels(context.Context) ([]PublicModel, error)
}
