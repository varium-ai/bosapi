package infrastructure

import (
	"encoding/json"

	"bosapi/backend/internal/user/domain"
	"gorm.io/gorm"
)

// A key filter can only narrow channels already assigned to its organization.
func validateKeyProviders(tx *gorm.DB, organizationID uint64, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	var count int64
	err := tx.Table("accounts").Where("accounts.id IN ?", ids).
		Where("EXISTS (SELECT 1 FROM organization_provider_bindings b WHERE b.account_id = accounts.id AND b.organization_id = ? AND b.status = ?)", organizationID, "active").Count(&count).Error
	if err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return gorm.ErrInvalidData
	}
	return nil
}

func encodeKeyAccessUpdates(tx *gorm.DB, organizationID uint64, updates map[string]any) error {
	var providers []uint64
	var models []string
	if value, ok := updates["allowed_provider_ids"]; ok {
		var valid bool
		providers, valid = value.([]uint64)
		if !valid {
			return gorm.ErrInvalidData
		}
	}
	if value, ok := updates["allowed_models"]; ok {
		var valid bool
		models, valid = value.([]string)
		if !valid {
			return gorm.ErrInvalidData
		}
	}
	for _, field := range []string{"ip_allowlist", "ip_blocklist"} {
		if value, supplied := updates[field]; supplied {
			if _, valid := value.([]string); !valid {
				return gorm.ErrInvalidData
			}
		}
	}
	if err := domain.ValidateKeyAccessPolicy(providers, models); err != nil {
		return gorm.ErrInvalidData
	}
	if err := validateKeyProviders(tx, organizationID, providers); err != nil {
		return err
	}
	for _, field := range []string{"allowed_provider_ids", "allowed_models", "ip_allowlist", "ip_blocklist"} {
		if value, supplied := updates[field]; supplied {
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			updates[field] = string(encoded)
		}
	}
	return nil
}
