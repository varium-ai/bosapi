package infrastructure

import (
	"context"
	"time"

	"bosapi/backend/internal/auth/domain"
	"gorm.io/gorm"
)

// UserRecord is the Gorm representation of a portal user.
type UserRecord struct {
	ID                  uint64     `gorm:"primaryKey"`
	Email               string     `gorm:"size:320;uniqueIndex;not null"`
	PasswordHash        string     `gorm:"size:255;not null"`
	OrganizationID      uint64     `gorm:"index"`
	Role                string     `gorm:"size:32;not null"`
	Status              string     `gorm:"size:24;not null"`
	TOTPSecretEncrypted *string    `gorm:"column:totp_secret_encrypted"`
	TOTPEnabled         bool       `gorm:"column:totp_enabled;not null"`
	TOTPEnabledAt       *time.Time `gorm:"column:totp_enabled_at"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// TableName maps identity records to the shared users table.
func (UserRecord) TableName() string { return "users" }

// RefreshRecord stores a hash-only refresh session.
type RefreshRecord struct {
	ID        uint64    `gorm:"primaryKey"`
	UserID    uint64    `gorm:"index;not null"`
	TokenHash string    `gorm:"size:64;uniqueIndex;not null"`
	ExpiresAt time.Time `gorm:"index;not null"`
	RevokedAt *time.Time
	CreatedAt time.Time
}

// AccessRevocation stores a JWT identifier until its expiration.
type AccessRevocation struct {
	JTI       string    `gorm:"primaryKey;size:64"`
	ExpiresAt time.Time `gorm:"index;not null"`
}

type LoginLogRecord struct {
	ID        uint64    `gorm:"primaryKey"`
	UserID    *uint64   `gorm:"index"`
	Email     string    `gorm:"size:320;index"`
	ClientIP  string    `gorm:"size:45;not null"`
	Success   bool      `gorm:"not null"`
	Reason    string    `gorm:"size:64"`
	CreatedAt time.Time `gorm:"index;not null"`
}

func (LoginLogRecord) TableName() string { return "login_logs" }

// Repository implements authentication persistence using Gorm.
type Repository struct{ database *gorm.DB }

// NewRepository constructs the Gorm authentication repository.
func NewRepository(database *gorm.DB) *Repository { return &Repository{database: database} }

// FindByEmail finds an identity by normalized email.
func (repository *Repository) FindByEmail(ctx context.Context, email string) (domain.User, error) {
	var record UserRecord
	if err := repository.database.WithContext(ctx).Where("email = ?", email).First(&record).Error; err != nil {
		return domain.User{}, err
	}
	return userDomain(record), nil
}

func (repository *Repository) RecordLoginAttempt(ctx context.Context, email, ip string, success bool, reason string, at time.Time) error {
	record := LoginLogRecord{Email: email, ClientIP: ip, Success: success, Reason: reason, CreatedAt: at.UTC()}
	var user UserRecord
	if email != "" {
		if err := repository.database.WithContext(ctx).Select("id").Where("email = ?", email).First(&user).Error; err == nil {
			record.UserID = &user.ID
		}
	}
	return repository.database.WithContext(ctx).Create(&record).Error
}

// FindByID finds an identity by its stable identifier.
func (repository *Repository) FindByID(ctx context.Context, id uint64) (domain.User, error) {
	var record UserRecord
	if err := repository.database.WithContext(ctx).First(&record, id).Error; err != nil {
		return domain.User{}, err
	}
	return userDomain(record), nil
}

// FindRefreshUser finds the active owner of a valid refresh token digest.
func (repository *Repository) FindRefreshUser(ctx context.Context, hash string, now time.Time) (domain.User, error) {
	var session RefreshRecord
	if err := repository.database.WithContext(ctx).Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", hash, now).First(&session).Error; err != nil {
		return domain.User{}, err
	}
	return repository.FindByID(ctx, session.UserID)
}

// CreateRefresh stores a refresh token hash.
func (repository *Repository) CreateRefresh(ctx context.Context, session *domain.RefreshSession) error {
	record := refreshRecord(*session)
	if err := repository.database.WithContext(ctx).Create(&record).Error; err != nil {
		return err
	}
	session.ID = record.ID
	return nil
}

// RotateRefresh atomically revokes the old token and inserts the replacement.
func (repository *Repository) RotateRefresh(ctx context.Context, oldHash string, replacement *domain.RefreshSession, now time.Time) error {
	return repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		result := transaction.Model(&RefreshRecord{}).Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", oldHash, now).Update("revoked_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		record := refreshRecord(*replacement)
		return transaction.Create(&record).Error
	})
}

// RevokeRefresh invalidates a refresh token by its digest.
func (repository *Repository) RevokeRefresh(ctx context.Context, hash string, now time.Time) error {
	return repository.database.WithContext(ctx).Model(&RefreshRecord{}).Where("token_hash = ? AND revoked_at IS NULL", hash).Update("revoked_at", now).Error
}

// RevokeAccess records a JWT identifier until token expiration.
func (repository *Repository) RevokeAccess(ctx context.Context, jti string, expiry time.Time) error {
	return repository.database.WithContext(ctx).Create(&AccessRevocation{JTI: jti, ExpiresAt: expiry}).Error
}

// IsAccessRevoked reports whether the JWT identifier is on the denylist.
func (repository *Repository) IsAccessRevoked(ctx context.Context, jti string) (bool, error) {
	var count int64
	err := repository.database.WithContext(ctx).Model(&AccessRevocation{}).Where("jti = ? AND expires_at > ?", jti, time.Now().UTC()).Count(&count).Error
	return count > 0, err
}

// UpdatePassword updates an identity's password hash and timestamp.
func (repository *Repository) UpdatePassword(ctx context.Context, userID uint64, passwordHash string) error {
	now := time.Now().UTC()
	return repository.database.WithContext(ctx).Model(&UserRecord{}).Where("id = ?", userID).Updates(map[string]any{
		"password_hash": passwordHash,
		"updated_at":    now,
	}).Error
}

// RevokeAllUserRefresh revokes all active refresh sessions belonging to a user.
func (repository *Repository) RevokeAllUserRefresh(ctx context.Context, userID uint64, now time.Time) error {
	return repository.database.WithContext(ctx).Model(&RefreshRecord{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error
}

// EnableTOTP activates two-factor authentication with an encrypted secret.
func (repository *Repository) EnableTOTP(ctx context.Context, userID uint64, encryptedSecret string, enabledAt time.Time) error {
	now := time.Now().UTC()
	return repository.database.WithContext(ctx).Model(&UserRecord{}).Where("id = ?", userID).Updates(map[string]any{
		"totp_enabled":          true,
		"totp_secret_encrypted": encryptedSecret,
		"totp_enabled_at":       enabledAt,
		"updated_at":            now,
	}).Error
}

// DisableTOTP deactivates two-factor authentication and clears stored secret.
func (repository *Repository) DisableTOTP(ctx context.Context, userID uint64) error {
	now := time.Now().UTC()
	return repository.database.WithContext(ctx).Model(&UserRecord{}).Where("id = ?", userID).Updates(map[string]any{
		"totp_enabled":          false,
		"totp_secret_encrypted": nil,
		"totp_enabled_at":       nil,
		"updated_at":            now,
	}).Error
}

func userDomain(record UserRecord) domain.User {
	return domain.User{
		ID:                  record.ID,
		Email:               record.Email,
		PasswordHash:        record.PasswordHash,
		OrganizationID:      record.OrganizationID,
		Role:                record.Role,
		Status:              record.Status,
		TOTPSecretEncrypted: record.TOTPSecretEncrypted,
		TOTPEnabled:         record.TOTPEnabled,
		TOTPEnabledAt:       record.TOTPEnabledAt,
		CreatedAt:           record.CreatedAt,
		UpdatedAt:           record.UpdatedAt,
	}
}

func refreshRecord(session domain.RefreshSession) RefreshRecord {
	return RefreshRecord{ID: session.ID, UserID: session.UserID, TokenHash: session.TokenHash, ExpiresAt: session.ExpiresAt, RevokedAt: session.RevokedAt, CreatedAt: session.CreatedAt}
}
