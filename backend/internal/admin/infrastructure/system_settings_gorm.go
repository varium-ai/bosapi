package infrastructure

import (
	"context"
	"errors"
	"time"

	"bosapi/backend/internal/admin/domain"
	analyticsdomain "bosapi/backend/internal/analytics/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SystemSettingRepository persists generic system settings in the settings table.
type SystemSettingRepository struct {
	database *gorm.DB
}

// NewSystemSettingRepository constructs the GORM system settings repository.
func NewSystemSettingRepository(database *gorm.DB) *SystemSettingRepository {
	return &SystemSettingRepository{database: database}
}

// Get loads a setting by its unique key.
func (repo *SystemSettingRepository) Get(ctx context.Context, key string) (*domain.SystemSetting, error) {
	var record settingRecord
	err := repo.database.WithContext(ctx).Where("setting_key = ?", key).First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	val := ""
	if record.ValueText != nil {
		val = *record.ValueText
	}
	return &domain.SystemSetting{
		SettingKey: record.SettingKey,
		ValueText:  val,
		IsSecret:   record.IsSecret,
		UpdatedBy:  record.UpdatedBy,
		UpdatedAt:  record.UpdatedAt,
	}, nil
}

// Set upserts a setting record by its key.
func (repo *SystemSettingRepository) Set(ctx context.Context, key string, valueText string, isSecret bool, updatedBy *uint64) error {
	if analyticsdomain.ReservedSetting(key) {
		return errors.New("use the usage-reporting service")
	}
	record := settingRecord{
		SettingKey: key,
		ValueText:  &valueText,
		IsSecret:   isSecret,
		UpdatedBy:  updatedBy,
		UpdatedAt:  time.Now().UTC(),
	}
	return repo.database.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "setting_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value_text", "is_secret", "updated_by", "updated_at"}),
	}).Create(&record).Error
}

// Delete removes a setting record by its key.
func (repo *SystemSettingRepository) Delete(ctx context.Context, key string) error {
	if analyticsdomain.ReservedSetting(key) {
		return errors.New("use the usage-reporting service")
	}
	return repo.database.WithContext(ctx).Where("setting_key = ?", key).Delete(&settingRecord{}).Error
}

// List returns all settings matching a key prefix.
func (repo *SystemSettingRepository) List(ctx context.Context, prefix string) ([]domain.SystemSetting, error) {
	query := repo.database.WithContext(ctx).Model(&settingRecord{})
	if prefix != "" {
		query = query.Where("setting_key LIKE ?", prefix+"%")
	}
	var records []settingRecord
	if err := query.Order("setting_key ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	results := make([]domain.SystemSetting, len(records))
	for i, record := range records {
		val := ""
		if record.ValueText != nil {
			val = *record.ValueText
		}
		results[i] = domain.SystemSetting{
			SettingKey: record.SettingKey,
			ValueText:  val,
			IsSecret:   record.IsSecret,
			UpdatedBy:  record.UpdatedBy,
			UpdatedAt:  record.UpdatedAt,
		}
	}
	return results, nil
}

var _ domain.SystemSettingRepository = (*SystemSettingRepository)(nil)
