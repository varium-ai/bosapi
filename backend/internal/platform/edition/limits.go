// Package edition defines the resource capacity of the community distribution.
package edition

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

const (
	MaxOrganizations = 2
	MaxAPIKeys       = 10
	capacityLockKey  = "community.capacity_lock"
)

var ErrLimitExceeded = errors.New("community edition resource limit exceeded")

// ReservedSetting protects the serialization row from generic settings CRUD.
func ReservedSetting(key string) bool { return key == capacityLockKey }

// LimitError exposes only a resource name and the public capacity limit.
type LimitError struct {
	Resource string
	Limit    int
}

func (err *LimitError) Error() string {
	return fmt.Sprintf("community edition supports at most %d %s per installation", err.Limit, err.Resource)
}

func (err *LimitError) Unwrap() error { return ErrLimitExceeded }

// CheckCapacity must run inside the same transaction as the insertion. A shared
// row serializes creators across organizations, processes and batch imports.
// Disabled resources count; deleted resources release capacity. Existing records
// are never deleted or disabled when capacity is reached.
func CheckCapacity(tx *gorm.DB, resource string, incoming int) error {
	var limit int
	switch resource {
	case "organizations":
		limit = MaxOrganizations
	case "api_keys":
		limit = MaxAPIKeys
	default:
		return errors.New("unknown community capacity resource")
	}
	if incoming < 1 {
		return errors.New("capacity reservation must be positive")
	}
	var key string
	row := tx.Raw("SELECT setting_key FROM settings WHERE setting_key = ? FOR UPDATE", capacityLockKey).Row()
	if err := row.Scan(&key); err != nil {
		return fmt.Errorf("lock community capacity: %w", err)
	}
	var count int64
	if err := tx.Table(resource).Where("status <> ?", "deleted").Count(&count).Error; err != nil {
		return fmt.Errorf("read community capacity: %w", err)
	}
	if incoming > limit || count > int64(limit-incoming) {
		return &LimitError{Resource: resource, Limit: limit}
	}
	return nil
}
