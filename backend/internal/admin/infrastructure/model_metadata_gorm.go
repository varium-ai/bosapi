package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type modelMetadataRecord struct {
	ID                   uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	ModelID              string    `gorm:"column:model_id;uniqueIndex"`
	DisplayName          string    `gorm:"column:display_name"`
	InputModalitiesJSON  string    `gorm:"column:input_modalities;type:json"`
	OutputModalitiesJSON string    `gorm:"column:output_modalities;type:json"`
	SupportsVision       bool      `gorm:"column:supports_vision"`
	SupportsVideo        bool      `gorm:"column:supports_video"`
	SupportsAudio        bool      `gorm:"column:supports_audio"`
	SupportsTools        bool      `gorm:"column:supports_tools"`
	SupportsReasoning    bool      `gorm:"column:supports_reasoning"`
	ContextWindow        int       `gorm:"column:context_window"`
	MaxOutputTokens      int       `gorm:"column:max_output_tokens"`
	Source               string    `gorm:"column:source"`
	CreatedAt            time.Time `gorm:"column:created_at"`
	UpdatedAt            time.Time `gorm:"column:updated_at"`
}

func (modelMetadataRecord) TableName() string {
	return "model_metadata"
}

// ModelMetadataRepository implements domain.ModelMetadataRepository using GORM.
type ModelMetadataRepository struct {
	database *gorm.DB
}

// NewModelMetadataRepository constructs the GORM model metadata repository.
func NewModelMetadataRepository(database *gorm.DB) *ModelMetadataRepository {
	return &ModelMetadataRepository{database: database}
}

// Find finds a single model metadata entry by its model_id.
func (repo *ModelMetadataRepository) Find(ctx context.Context, modelID string) (*domain.ModelMetadata, error) {
	var record modelMetadataRecord
	err := repo.database.WithContext(ctx).Where("LOWER(model_id) = LOWER(?)", strings.TrimSpace(modelID)).First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	meta := toDomainModelMetadata(record)
	return &meta, nil
}

// List returns a page of model metadata matching search.
func (repo *ModelMetadataRepository) List(ctx context.Context, search string, page, pageSize int) ([]domain.ModelMetadata, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := repo.database.WithContext(ctx).Model(&modelMetadataRecord{})
	if trimmed := strings.TrimSpace(search); trimmed != "" {
		like := "%" + strings.ToLower(trimmed) + "%"
		query = query.Where("LOWER(model_id) LIKE ? OR LOWER(display_name) LIKE ?", like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []modelMetadataRecord
	offset := (page - 1) * pageSize
	if err := query.Order("id DESC").Offset(offset).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.ModelMetadata, 0, len(records))
	for _, rec := range records {
		items = append(items, toDomainModelMetadata(rec))
	}
	return items, total, nil
}

// LookupCapabilities searches for capabilities by model name, supporting prefix and short-name matching.
func (repo *ModelMetadataRepository) LookupCapabilities(ctx context.Context, modelName string) (*domain.ModelMetadata, bool) {
	cleaned := strings.TrimSpace(modelName)
	if cleaned == "" {
		return nil, false
	}
	var record modelMetadataRecord

	// 1. Exact match (case insensitive)
	if err := repo.database.WithContext(ctx).Where("LOWER(model_id) = LOWER(?)", cleaned).First(&record).Error; err == nil {
		meta := toDomainModelMetadata(record)
		return &meta, true
	}

	// 2. If modelName has organization prefix (e.g. zai-org/GLM-5.3-Flash -> GLM-5.3-Flash)
	if idx := strings.LastIndex(cleaned, "/"); idx >= 0 && idx < len(cleaned)-1 {
		shortName := cleaned[idx+1:]
		if err := repo.database.WithContext(ctx).Where("LOWER(model_id) = LOWER(?)", shortName).First(&record).Error; err == nil {
			meta := toDomainModelMetadata(record)
			return &meta, true
		}
	}

	// 3. If modelName is short, check if table has prefixed version (e.g. table has zai-org/GLM-5.3-Flash, model is GLM-5.3-Flash)
	if err := repo.database.WithContext(ctx).Where("LOWER(model_id) LIKE ?", "%/"+strings.ToLower(cleaned)).First(&record).Error; err == nil {
		meta := toDomainModelMetadata(record)
		return &meta, true
	}

	return nil, false
}

// Upsert creates or updates a single model metadata entry.
func (repo *ModelMetadataRepository) Upsert(ctx context.Context, item *domain.ModelMetadata) error {
	record := toRecordModelMetadata(*item)
	return repo.database.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "model_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "input_modalities", "output_modalities",
			"supports_vision", "supports_video", "supports_audio",
			"supports_tools", "supports_reasoning", "context_window",
			"max_output_tokens", "source", "updated_at",
		}),
	}).Create(&record).Error
}

// BatchUpsert performs batch upserts for model metadata.
func (repo *ModelMetadataRepository) BatchUpsert(ctx context.Context, items []domain.ModelMetadata) error {
	if len(items) == 0 {
		return nil
	}
	records := make([]modelMetadataRecord, 0, len(items))
	for _, it := range items {
		records = append(records, toRecordModelMetadata(it))
	}
	return repo.database.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "model_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "input_modalities", "output_modalities",
			"supports_vision", "supports_video", "supports_audio",
			"supports_tools", "supports_reasoning", "context_window",
			"max_output_tokens", "source", "updated_at",
		}),
	}).CreateInBatches(records, 100).Error
}

func toDomainModelMetadata(record modelMetadataRecord) domain.ModelMetadata {
	var inMods, outMods []string
	if record.InputModalitiesJSON != "" {
		_ = json.Unmarshal([]byte(record.InputModalitiesJSON), &inMods)
	}
	if record.OutputModalitiesJSON != "" {
		_ = json.Unmarshal([]byte(record.OutputModalitiesJSON), &outMods)
	}
	return domain.ModelMetadata{
		ID:                record.ID,
		ModelID:           record.ModelID,
		DisplayName:       record.DisplayName,
		InputModalities:   inMods,
		OutputModalities:  outMods,
		SupportsVision:    record.SupportsVision,
		SupportsVideo:     record.SupportsVideo,
		SupportsAudio:     record.SupportsAudio,
		SupportsTools:     record.SupportsTools,
		SupportsReasoning: record.SupportsReasoning,
		ContextWindow:     record.ContextWindow,
		MaxOutputTokens:   record.MaxOutputTokens,
		Source:            record.Source,
		CreatedAt:         record.CreatedAt,
		UpdatedAt:         record.UpdatedAt,
	}
}

func toRecordModelMetadata(item domain.ModelMetadata) modelMetadataRecord {
	inJSON, _ := json.Marshal(item.InputModalities)
	outJSON, _ := json.Marshal(item.OutputModalities)
	now := time.Now().UTC()
	return modelMetadataRecord{
		ModelID:              item.ModelID,
		DisplayName:          item.DisplayName,
		InputModalitiesJSON:  string(inJSON),
		OutputModalitiesJSON: string(outJSON),
		SupportsVision:       item.SupportsVision,
		SupportsVideo:        item.SupportsVideo,
		SupportsAudio:        item.SupportsAudio,
		SupportsTools:        item.SupportsTools,
		SupportsReasoning:    item.SupportsReasoning,
		ContextWindow:        item.ContextWindow,
		MaxOutputTokens:      item.MaxOutputTokens,
		Source:               item.Source,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}
