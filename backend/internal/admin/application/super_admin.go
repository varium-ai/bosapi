package application

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
)

var (
	// ErrInvalidSuperAdminRequest identifies an invalid administrator creation request.
	ErrInvalidSuperAdminRequest = errors.New("invalid super admin request")
	// ErrInvalidOTP identifies an expired, incorrect, or consumed one-time code.
	ErrInvalidOTP = errors.New("invalid or expired otp")
	// ErrOTPDeliveryUnavailable identifies missing or failed OTP delivery configuration.
	ErrOTPDeliveryUnavailable = errors.New("otp delivery unavailable")
)

// OTPNotifier sends one-time codes without returning delivery details to clients.
type OTPNotifier interface {
	SendOTP(context.Context, string, string) error
}

// SuperAdminService coordinates short-lived OTP verification and administrator creation.
type SuperAdminService struct {
	repository   domain.SuperAdminRepository
	notifier     OTPNotifier
	passwordHash func(string) (string, error)
	hmacKey      []byte
	now          func() time.Time
}

// NewSuperAdminService constructs privileged administrator operations.
func NewSuperAdminService(repository domain.SuperAdminRepository, notifier OTPNotifier, passwordHash func(string) (string, error), hmacKey []byte) *SuperAdminService {
	return &SuperAdminService{repository: repository, notifier: notifier, passwordHash: passwordHash, hmacKey: append([]byte(nil), hmacKey...), now: time.Now}
}

// StartOTP creates a five-minute challenge and sends its code through the configured notifier.
func (service *SuperAdminService) StartOTP(ctx context.Context, adminID uint64, email string) error {
	email, err := normalizeEmail(email)
	if adminID == 0 || err != nil || service.notifier == nil || len(service.hmacKey) < 32 {
		return ErrOTPDeliveryUnavailable
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return fmt.Errorf("generate administrator otp: %w", err)
	}
	code := fmt.Sprintf("%06d", number.Int64())
	now := service.now().UTC()
	challenge, err := service.repository.CreateOTPChallenge(ctx, adminID, email, service.codeHash(adminID, email, code), now.Add(5*time.Minute))
	if errors.Is(err, domain.ErrOTPRequestTooSoon) {
		return ErrOTPDeliveryUnavailable
	}
	if errors.Is(err, domain.ErrAdminEmailExists) {
		return ErrInvalidSuperAdminRequest
	}
	if err != nil {
		return fmt.Errorf("create otp challenge: %w", err)
	}
	if err := service.notifier.SendOTP(ctx, email, code); err != nil {
		if invalidateErr := service.repository.InvalidateOTPChallenge(ctx, challenge.ID); invalidateErr != nil {
			return fmt.Errorf("send otp and invalidate challenge: %w", errors.Join(err, invalidateErr))
		}
		return ErrOTPDeliveryUnavailable
	}
	return nil
}

// CreateAdmin validates optional email verification before creating an administrator.
func (service *SuperAdminService) CreateAdmin(ctx context.Context, issuerID uint64, input domain.SuperAdminInput) error {
	email, err := normalizeEmail(input.Email)
	input.OTP = strings.TrimSpace(input.OTP)
	if issuerID == 0 || err != nil || input.OTP != "" && (len(input.OTP) != 6 || !digitsOnly(input.OTP) || len(service.hmacKey) < 32) || input.Role == "org_admin" && input.OrganizationID == 0 || (input.Role != "org_admin" && input.Role != "super_admin") || service.passwordHash == nil {
		return ErrInvalidSuperAdminRequest
	}
	if input.Role == "super_admin" {
		input.OrganizationID = 0
	}
	passwordHash, err := service.passwordHash(input.Password)
	if err != nil {
		return ErrInvalidSuperAdminRequest
	}
	candidateHash := ""
	if input.OTP != "" {
		candidateHash = service.codeHash(issuerID, email, input.OTP)
	}
	err = service.repository.CreateAdminWithOTP(ctx, issuerID, email, candidateHash, passwordHash, input.OrganizationID, input.Role, service.now().UTC())
	if errors.Is(err, domain.ErrInvalidOTPChallenge) {
		return ErrInvalidOTP
	}
	if errors.Is(err, domain.ErrAdminEmailExists) {
		return ErrInvalidSuperAdminRequest
	}
	if err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	return nil
}

func (service *SuperAdminService) codeHash(adminID uint64, email, code string) string {
	mac := hmac.New(sha256.New, service.hmacKey)
	_, _ = fmt.Fprintf(mac, "%d\x00%s\x00%s", adminID, email, code)
	return hex.EncodeToString(mac.Sum(nil))
}

func normalizeEmail(value string) (string, error) {
	// Preserve the address casing supplied by the administrator. Email lookup
	// remains case-insensitive in the authentication flow and database collation.
	value = strings.TrimSpace(value)
	if len(value) > 320 {
		return "", ErrInvalidSuperAdminRequest
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || !strings.Contains(value, "@") {
		return "", ErrInvalidSuperAdminRequest
	}
	return value, nil
}
func digitsOnly(value string) bool {
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
