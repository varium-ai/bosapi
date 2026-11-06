package infrastructure

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"bosapi/backend/internal/admin/domain"
	authinfra "bosapi/backend/internal/auth/infrastructure"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type otpChallengeRecord struct {
	ID          uint64    `gorm:"primaryKey"`
	AdminID     uint64    `gorm:"index;not null"`
	TargetEmail string    `gorm:"size:320;not null"`
	CodeHash    string    `gorm:"size:64;not null"`
	Attempts    uint16    `gorm:"not null;default:0"`
	ExpiresAt   time.Time `gorm:"index;not null"`
	ConsumedAt  *time.Time
	CreatedAt   time.Time
}

func (otpChallengeRecord) TableName() string { return "admin_otp_challenges" }

// SuperAdminRepository persists OTP challenges and administrator creation transactions.
type SuperAdminRepository struct{ database *gorm.DB }

// NewSuperAdminRepository constructs the Gorm super-admin repository.
func NewSuperAdminRepository(database *gorm.DB) *SuperAdminRepository {
	return &SuperAdminRepository{database: database}
}

// CreateOTPChallenge invalidates prior challenges before issuing one for an active super-admin.
func (repository *SuperAdminRepository) CreateOTPChallenge(ctx context.Context, adminID uint64, email, hash string, expires time.Time) (domain.AdminOTPChallenge, error) {
	var record otpChallengeRecord
	err := repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		var issuer authinfra.UserRecord
		if err := transaction.Where("id = ? AND role = ? AND status = ?", adminID, "super_admin", "active").First(&issuer).Error; err != nil {
			return err
		}
		var existing authinfra.UserRecord
		if err := transaction.Where("email = ?", email).First(&existing).Error; err == nil {
			return domain.ErrAdminEmailExists
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now().UTC()
		var recent otpChallengeRecord
		if err := transaction.Where("admin_id = ? AND consumed_at IS NULL AND created_at > ?", adminID, now.Add(-30*time.Second)).First(&recent).Error; err == nil {
			return domain.ErrOTPRequestTooSoon
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := transaction.Model(&otpChallengeRecord{}).Where("admin_id = ? AND consumed_at IS NULL", adminID).Update("consumed_at", now).Error; err != nil {
			return err
		}
		record = otpChallengeRecord{AdminID: adminID, TargetEmail: email, CodeHash: hash, ExpiresAt: expires, CreatedAt: now}
		return transaction.Create(&record).Error
	})
	if err != nil {
		return domain.AdminOTPChallenge{}, err
	}
	return domain.AdminOTPChallenge{ID: record.ID, AdminID: adminID, TargetEmail: email, ExpiresAt: expires}, nil
}

// InvalidateOTPChallenge consumes a challenge after delivery failure.
func (repository *SuperAdminRepository) InvalidateOTPChallenge(ctx context.Context, id uint64) error {
	now := time.Now().UTC()
	return repository.database.WithContext(ctx).Model(&otpChallengeRecord{}).Where("id = ? AND consumed_at IS NULL", id).Update("consumed_at", now).Error
}

// CreateAdminWithOTP permits omission but atomically verifies any supplied OTP before creating a user.
func (repository *SuperAdminRepository) CreateAdminWithOTP(ctx context.Context, issuerID uint64, email, candidateHash, passwordHash string, organizationID uint64, role string, now time.Time) error {
	invalid := false
	err := repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		var issuer authinfra.UserRecord
		if err := transaction.Where("id = ? AND role = ? AND status = ?", issuerID, "super_admin", "active").First(&issuer).Error; err != nil {
			return err
		}
		if candidateHash != "" {
			valid, err := consumeAdminOTP(transaction, issuerID, email, candidateHash, now)
			if err != nil {
				return err
			}
			if !valid {
				invalid = true
				return nil
			}
		}
		if role != "super_admin" {
			var organization OrganizationRecord
			if err := transaction.Where("id = ? AND status = ?", organizationID, "active").First(&organization).Error; err != nil {
				return err
			}
		}
		var existing authinfra.UserRecord
		if err := transaction.Where("email = ?", email).First(&existing).Error; err == nil {
			return domain.ErrAdminEmailExists
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		user := authinfra.UserRecord{Email: email, PasswordHash: passwordHash, OrganizationID: organizationID, Role: role, Status: "active", CreatedAt: now, UpdatedAt: now}
		if role == "super_admin" {
			return transaction.Omit("OrganizationID").Create(&user).Error
		}
		return transaction.Create(&user).Error
	})
	if err != nil {
		return err
	}
	if invalid {
		return domain.ErrInvalidOTPChallenge
	}
	return nil
}

func consumeAdminOTP(transaction *gorm.DB, issuerID uint64, email, candidateHash string, now time.Time) (bool, error) {
	var challenge otpChallengeRecord
	err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("admin_id = ? AND target_email = ? AND consumed_at IS NULL AND expires_at > ?", issuerID, email, now).Order("created_at DESC").First(&challenge).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if challenge.Attempts >= 5 {
		return false, nil
	}
	if subtle.ConstantTimeCompare([]byte(challenge.CodeHash), []byte(candidateHash)) != 1 {
		challenge.Attempts++
		// Gorm requires dynamic columns because only exhausted challenges are consumed.
		updates := map[string]any{"attempts": challenge.Attempts}
		if challenge.Attempts >= 5 {
			updates["consumed_at"] = now
		}
		if err := transaction.Model(&challenge).Updates(updates).Error; err != nil {
			return false, err
		}
		return false, nil
	}
	if err := transaction.Model(&challenge).Update("consumed_at", now).Error; err != nil {
		return false, err
	}
	return true, nil
}

var _ domain.SuperAdminRepository = (*SuperAdminRepository)(nil)
