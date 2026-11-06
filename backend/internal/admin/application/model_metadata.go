package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
)

// ModelMetadataService coordinates model capabilities reference updates and queries.
type ModelMetadataService struct {
	repository domain.ModelMetadataRepository
	httpClient *http.Client
}

// NewModelMetadataService constructs model metadata service operations.
func NewModelMetadataService(repository domain.ModelMetadataRepository) *ModelMetadataService {
	return &ModelMetadataService{
		repository: repository,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// List returns paginated model metadata matching search query.
func (service *ModelMetadataService) List(ctx context.Context, search string, page, pageSize int) ([]domain.ModelMetadata, int64, error) {
	return service.repository.List(ctx, search, page, pageSize)
}

// Upsert creates or manually updates model metadata.
func (service *ModelMetadataService) Upsert(ctx context.Context, item domain.ModelMetadata) error {
	if strings.TrimSpace(item.ModelID) == "" {
		return fmt.Errorf("model_id is required")
	}
	item.ModelID = strings.TrimSpace(item.ModelID)
	if item.Source == "" {
		item.Source = "manual"
	}
	return service.repository.Upsert(ctx, &item)
}

type openRouterModelsResponse struct {
	Data []struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		ContextLen   int    `json:"context_length"`
		Architecture struct {
			Modality         string   `json:"modality"`
			InputModalities  []string `json:"input_modalities"`
			OutputModalities []string `json:"output_modalities"`
		} `json:"architecture"`
		TopProvider struct {
			MaxCompletionTokens int `json:"max_completion_tokens"`
		} `json:"top_provider"`
		SupportedParameters []string `json:"supported_parameters"`
	} `json:"data"`
}

// SyncFromOpenRouter fetches live model specs from OpenRouter public API.
func (service *ModelMetadataService) SyncFromOpenRouter(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return 0, fmt.Errorf("create openrouter request: %w", err)
	}
	req.Header.Set("User-Agent", "bossapi-model-sync/1.0")

	res, err := service.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch openrouter models: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("openrouter returned status: %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, 10<<20))
	if err != nil {
		return 0, fmt.Errorf("read openrouter response: %w", err)
	}

	var payload openRouterModelsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("parse openrouter json: %w", err)
	}

	items := make([]domain.ModelMetadata, 0, len(payload.Data))
	for _, m := range payload.Data {
		name := strings.TrimSpace(m.ID)
		if name == "" {
			continue
		}
		inMods := m.Architecture.InputModalities
		if len(inMods) == 0 {
			inMods = []string{"text"}
		}
		outMods := m.Architecture.OutputModalities
		if len(outMods) == 0 {
			outMods = []string{"text"}
		}

		supportsVision := slices.Contains(inMods, "image")
		supportsVideo := slices.Contains(inMods, "video")
		supportsAudio := slices.Contains(inMods, "audio")
		supportsTools := slices.Contains(m.SupportedParameters, "tools")
		supportsReasoning := strings.Contains(strings.ToLower(name), "r1") ||
			strings.Contains(strings.ToLower(name), "reason") ||
			strings.Contains(strings.ToLower(name), "o1") ||
			strings.Contains(strings.ToLower(name), "o3")

		items = append(items, domain.ModelMetadata{
			ModelID:           name,
			DisplayName:       m.Name,
			InputModalities:   inMods,
			OutputModalities:  outMods,
			SupportsVision:    supportsVision,
			SupportsVideo:     supportsVideo,
			SupportsAudio:     supportsAudio,
			SupportsTools:     supportsTools,
			SupportsReasoning: supportsReasoning,
			ContextWindow:     m.ContextLen,
			MaxOutputTokens:   m.TopProvider.MaxCompletionTokens,
			Source:            "openrouter",
		})
	}

	if len(items) > 0 {
		if err := service.repository.BatchUpsert(ctx, items); err != nil {
			return 0, fmt.Errorf("batch upsert openrouter models: %w", err)
		}
	}

	return len(items), nil
}

type liteLLMModelEntry struct {
	Mode                    string `json:"mode"`
	SupportsVision          bool   `json:"supports_vision"`
	SupportsFunctionCalling bool   `json:"supports_function_calling"`
	SupportsAudioInput      bool   `json:"supports_audio_input"`
	SupportsAudioOutput     bool   `json:"supports_audio_output"`
	MaxInputTokens          int    `json:"max_input_tokens"`
	MaxOutputTokens         int    `json:"max_output_tokens"`
}

// SyncFromLiteLLM fetches community-maintained model specs from LiteLLM repository.
func (service *ModelMetadataService) SyncFromLiteLLM(ctx context.Context) (int, error) {
	url := "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create litellm request: %w", err)
	}

	res, err := service.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch litellm models: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("litellm returned status: %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, 15<<20))
	if err != nil {
		return 0, fmt.Errorf("read litellm response: %w", err)
	}

	var rawMap map[string]liteLLMModelEntry
	if err := json.Unmarshal(body, &rawMap); err != nil {
		return 0, fmt.Errorf("parse litellm json: %w", err)
	}

	items := make([]domain.ModelMetadata, 0, len(rawMap))
	for modelID, entry := range rawMap {
		cleanID := strings.TrimSpace(modelID)
		if cleanID == "" || strings.HasPrefix(cleanID, "sample_spec") {
			continue
		}
		inMods := []string{"text"}
		if entry.SupportsVision {
			inMods = append(inMods, "image")
		}
		if entry.SupportsAudioInput {
			inMods = append(inMods, "audio")
		}
		outMods := []string{"text"}
		if entry.SupportsAudioOutput {
			outMods = append(outMods, "audio")
		}

		lower := strings.ToLower(cleanID)
		supportsReasoning := strings.Contains(lower, "r1") ||
			strings.Contains(lower, "reason") ||
			strings.Contains(lower, "o1") ||
			strings.Contains(lower, "o3")

		items = append(items, domain.ModelMetadata{
			ModelID:           cleanID,
			DisplayName:       cleanID,
			InputModalities:   inMods,
			OutputModalities:  outMods,
			SupportsVision:    entry.SupportsVision,
			SupportsVideo:     false,
			SupportsAudio:     entry.SupportsAudioInput,
			SupportsTools:     entry.SupportsFunctionCalling,
			SupportsReasoning: supportsReasoning,
			ContextWindow:     entry.MaxInputTokens,
			MaxOutputTokens:   entry.MaxOutputTokens,
			Source:            "litellm",
		})
	}

	if len(items) > 0 {
		if err := service.repository.BatchUpsert(ctx, items); err != nil {
			return 0, fmt.Errorf("batch upsert litellm models: %w", err)
		}
	}

	return len(items), nil
}
