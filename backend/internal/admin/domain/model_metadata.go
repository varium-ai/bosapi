package domain

import (
	"context"
	"time"
)

// ModelMetadata represents global model capabilities and parameters.
type ModelMetadata struct {
	ID                uint64    `json:"id"`
	ModelID           string    `json:"model_id"`
	DisplayName       string    `json:"display_name"`
	InputModalities   []string  `json:"input_modalities"`
	OutputModalities  []string  `json:"output_modalities"`
	SupportsVision    bool      `json:"supports_vision"`
	SupportsVideo     bool      `json:"supports_video"`
	SupportsAudio     bool      `json:"supports_audio"`
	SupportsTools     bool      `json:"supports_tools"`
	SupportsReasoning bool      `json:"supports_reasoning"`
	ContextWindow     int       `json:"context_window"`
	MaxOutputTokens   int       `json:"max_output_tokens"`
	Source            string    `json:"source"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ModelMetadataRepository defines persistence operations for model metadata knowledge base.
type ModelMetadataRepository interface {
	Find(ctx context.Context, modelID string) (*ModelMetadata, error)
	List(ctx context.Context, search string, page, pageSize int) ([]ModelMetadata, int64, error)
	BatchUpsert(ctx context.Context, items []ModelMetadata) error
	Upsert(ctx context.Context, item *ModelMetadata) error
	LookupCapabilities(ctx context.Context, modelName string) (*ModelMetadata, bool)
}
