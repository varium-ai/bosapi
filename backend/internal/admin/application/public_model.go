package application

import (
	"context"
	"fmt"

	"bosapi/backend/internal/admin/domain"
)

// PublicModelService exposes only models admitted by active organization policies.
type PublicModelService struct{ repository domain.PublicModelRepository }

// NewPublicModelService constructs the public model catalog service.
func NewPublicModelService(repository domain.PublicModelRepository) *PublicModelService {
	return &PublicModelService{repository: repository}
}

// List returns deduplicated, safe public model metadata.
func (service *PublicModelService) List(ctx context.Context) ([]domain.PublicModel, error) {
	models, err := service.repository.ListPublicModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list public models: %w", err)
	}
	return models, nil
}
