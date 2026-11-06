package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/auth/domain"
	"bosapi/backend/internal/platform/totp"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct-horse-battery") {
		t.Fatal("valid password did not verify")
	}
	if VerifyPassword(hash, "incorrect-password") {
		t.Fatal("invalid password verified")
	}
}

func TestHashPasswordRejectsWeakPasswords(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected weak password to be rejected")
	}
}

type mockAuthRepository struct {
	users          map[string]domain.User
	usersByID      map[uint64]domain.User
	updatedPass    map[uint64]string
	revokedRefresh []uint64
}

func (m *mockAuthRepository) FindByEmail(ctx context.Context, email string) (domain.User, error) {
	u, ok := m.users[strings.ToLower(email)]
	if !ok {
		return domain.User{}, errors.New("user not found")
	}
	return u, nil
}

func (m *mockAuthRepository) FindByID(ctx context.Context, id uint64) (domain.User, error) {
	u, ok := m.usersByID[id]
	if !ok {
		return domain.User{}, errors.New("user not found")
	}
	return u, nil
}

func (m *mockAuthRepository) FindRefreshUser(context.Context, string, time.Time) (domain.User, error) {
	return domain.User{}, nil
}

func (m *mockAuthRepository) CreateRefresh(context.Context, *domain.RefreshSession) error {
	return nil
}

func (m *mockAuthRepository) RotateRefresh(context.Context, string, *domain.RefreshSession, time.Time) error {
	return nil
}

func (m *mockAuthRepository) RevokeRefresh(context.Context, string, time.Time) error {
	return nil
}

func (m *mockAuthRepository) RevokeAccess(context.Context, string, time.Time) error {
	return nil
}

func (m *mockAuthRepository) IsAccessRevoked(context.Context, string) (bool, error) {
	return false, nil
}

func (m *mockAuthRepository) UpdatePassword(ctx context.Context, userID uint64, hash string) error {
	m.updatedPass[userID] = hash
	u := m.usersByID[userID]
	u.PasswordHash = hash
	m.usersByID[userID] = u
	m.users[strings.ToLower(u.Email)] = u
	return nil
}

func (m *mockAuthRepository) RevokeAllUserRefresh(ctx context.Context, userID uint64, now time.Time) error {
	m.revokedRefresh = append(m.revokedRefresh, userID)
	return nil
}

func (m *mockAuthRepository) EnableTOTP(ctx context.Context, userID uint64, encryptedSecret string, enabledAt time.Time) error {
	u, ok := m.usersByID[userID]
	if !ok {
		return errors.New("user not found")
	}
	u.TOTPEnabled = true
	u.TOTPSecretEncrypted = &encryptedSecret
	u.TOTPEnabledAt = &enabledAt
	m.usersByID[userID] = u
	m.users[strings.ToLower(u.Email)] = u
	return nil
}

func (m *mockAuthRepository) DisableTOTP(ctx context.Context, userID uint64) error {
	u, ok := m.usersByID[userID]
	if !ok {
		return errors.New("user not found")
	}
	u.TOTPEnabled = false
	u.TOTPSecretEncrypted = nil
	u.TOTPEnabledAt = nil
	m.usersByID[userID] = u
	m.users[strings.ToLower(u.Email)] = u
	return nil
}

func (m *mockAuthRepository) RecordLoginAttempt(context.Context, string, string, bool, string, time.Time) error {
	return nil
}

func TestForgotPasswordAndResetFlow(t *testing.T) {
	initialHash, err := HashPassword("old-secret-password-123")
	if err != nil {
		t.Fatal(err)
	}

	testUser := domain.User{
		ID:           42,
		Email:        "user@example.com",
		PasswordHash: initialHash,
		Role:         "super_admin",
		Status:       "active",
	}

	repo := &mockAuthRepository{
		users:       map[string]domain.User{"user@example.com": testUser},
		usersByID:   map[uint64]domain.User{42: testUser},
		updatedPass: make(map[uint64]string),
	}

	secret := []byte("01234567890123456789012345678901")
	svc := NewService(repo, secret, 15*time.Minute, 7*24*time.Hour)

	// 1. Request reset for non-existing email
	_, _, err = svc.RequestPasswordReset(context.Background(), "unknown@example.com")
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}

	// 2. Request reset for valid email (without notifier, returns dev code)
	expiresIn, devCode, err := svc.RequestPasswordReset(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("unexpected error requesting reset: %v", err)
	}
	if expiresIn != 300 || len(devCode) != 6 {
		t.Fatalf("expected 300s expiry and 6-digit code, got %d, %s", expiresIn, devCode)
	}

	// 3. Request immediately again - should be rate limited
	_, _, err = svc.RequestPasswordReset(context.Background(), "user@example.com")
	if err == nil {
		t.Fatal("expected rate limit error")
	}

	// 4. Reset with wrong OTP
	err = svc.ResetPasswordWithOTP(context.Background(), "user@example.com", "000000", "new-secret-password-456")
	if err == nil {
		t.Fatal("expected error with wrong OTP")
	}

	// 5. Reset with invalid weak password
	err = svc.ResetPasswordWithOTP(context.Background(), "user@example.com", devCode, "short")
	if err == nil {
		t.Fatal("expected error with weak password")
	}

	// 6. Reset with correct OTP and valid password
	err = svc.ResetPasswordWithOTP(context.Background(), "user@example.com", devCode, "new-secret-password-456")
	if err != nil {
		t.Fatalf("unexpected error resetting password: %v", err)
	}

	// Verify updated password verifies against new password
	updatedHash := repo.updatedPass[42]
	if !VerifyPassword(updatedHash, "new-secret-password-456") {
		t.Fatal("new password does not verify")
	}
	if len(repo.revokedRefresh) != 1 || repo.revokedRefresh[0] != 42 {
		t.Fatal("expected refresh tokens to be revoked")
	}

	// 7. Verify OTP cannot be reused
	err = svc.ResetPasswordWithOTP(context.Background(), "user@example.com", devCode, "another-valid-password-789")
	if err == nil {
		t.Fatal("expected error reusing OTP")
	}
}

func TestChangePassword(t *testing.T) {
	initialHash, err := HashPassword("current-secret-password-123")
	if err != nil {
		t.Fatal(err)
	}

	testUser := domain.User{
		ID:           99,
		Email:        "admin@example.com",
		PasswordHash: initialHash,
		Role:         "super_admin",
		Status:       "active",
	}

	repo := &mockAuthRepository{
		users:       map[string]domain.User{"admin@example.com": testUser},
		usersByID:   map[uint64]domain.User{99: testUser},
		updatedPass: make(map[uint64]string),
	}

	secret := []byte("01234567890123456789012345678901")
	svc := NewService(repo, secret, 15*time.Minute, 7*24*time.Hour)

	// 1. Wrong current password
	err = svc.ChangePassword(context.Background(), 99, "wrong-password", "brand-new-password-123")
	if err == nil {
		t.Fatal("expected error for wrong current password")
	}

	// 2. Weak new password
	err = svc.ChangePassword(context.Background(), 99, "current-secret-password-123", "short")
	if err == nil {
		t.Fatal("expected error for weak new password")
	}

	// 3. Valid change
	err = svc.ChangePassword(context.Background(), 99, "current-secret-password-123", "brand-new-password-123")
	if err != nil {
		t.Fatalf("unexpected error changing password: %v", err)
	}

	if !VerifyPassword(repo.updatedPass[99], "brand-new-password-123") {
		t.Fatal("changed password does not verify")
	}
}

func TestMFALoginFlow(t *testing.T) {
	passwordHash, err := HashPassword("secure-password-123")
	if err != nil {
		t.Fatal(err)
	}

	secret := []byte("01234567890123456789012345678901")

	// 1. User without MFA should login directly
	userWithoutMFA := domain.User{
		ID:           101,
		Email:        "regular@example.com",
		PasswordHash: passwordHash,
		Role:         "org_admin",
		Status:       "active",
		TOTPEnabled:  false,
	}

	repo := &mockAuthRepository{
		users:     map[string]domain.User{"regular@example.com": userWithoutMFA},
		usersByID: map[uint64]domain.User{101: userWithoutMFA},
	}

	svc := NewService(repo, secret, 15*time.Minute, 7*24*time.Hour)

	loginResp, err := svc.Login(context.Background(), "regular@example.com", "secure-password-123")
	if err != nil {
		t.Fatalf("unexpected login error: %v", err)
	}
	if loginResp.MFARequired {
		t.Fatal("expected MFARequired to be false")
	}
	if loginResp.AccessToken == "" {
		t.Fatal("expected access token to be populated")
	}

	// 2. User with MFA enabled should get MFARequired: true and an mfa_token
	totpSecret, _ := totp.GenerateSecret()
	cipher, _ := newDefaultCipher(secret)
	encryptedSecret, _ := cipher.Encrypt(totpSecret)

	userWithMFA := domain.User{
		ID:                  102,
		Email:               "mfa-user@example.com",
		PasswordHash:        passwordHash,
		Role:                "super_admin",
		Status:              "active",
		TOTPEnabled:         true,
		TOTPSecretEncrypted: &encryptedSecret,
	}

	repo.users["mfa-user@example.com"] = userWithMFA
	repo.usersByID[102] = userWithMFA

	loginResp2, err := svc.Login(context.Background(), "mfa-user@example.com", "secure-password-123")
	if err != nil {
		t.Fatalf("unexpected login error: %v", err)
	}
	if !loginResp2.MFARequired {
		t.Fatal("expected MFARequired to be true")
	}
	if loginResp2.MFAToken == "" {
		t.Fatal("expected MFAToken to be present")
	}
	if loginResp2.AccessToken != "" {
		t.Fatal("access token should not be issued before MFA verification")
	}

	// 3. Verify MFA with invalid code should fail
	_, err = svc.VerifyMFA(context.Background(), loginResp2.MFAToken, "000000")
	if err == nil {
		t.Fatal("expected error with invalid MFA code")
	}

	// 4. Verify MFA with valid code should issue TokenPair
	now := time.Now().UTC()
	validCode, err := totp.GenerateCode(totpSecret, now)
	if err != nil {
		t.Fatal(err)
	}

	pair, err := svc.VerifyMFA(context.Background(), loginResp2.MFAToken, validCode)
	if err != nil {
		t.Fatalf("unexpected error on valid verify: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("expected valid access token")
	}

	// 5. Replay should be prevented
	_, err = svc.VerifyMFA(context.Background(), loginResp2.MFAToken, validCode)
	if err == nil {
		t.Fatal("expected replay protection to reject reused code")
	}
}

func TestMFASetupEnableDisableFlow(t *testing.T) {
	passwordHash, err := HashPassword("my-super-secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}

	secret := []byte("01234567890123456789012345678901")
	user := domain.User{
		ID:           201,
		Email:        "admin@example.com",
		PasswordHash: passwordHash,
		Role:         "super_admin",
		Status:       "active",
		TOTPEnabled:  false,
	}

	repo := &mockAuthRepository{
		users:     map[string]domain.User{"admin@example.com": user},
		usersByID: map[uint64]domain.User{201: user},
	}

	svc := NewService(repo, secret, 15*time.Minute, 7*24*time.Hour)

	// 1. Setup MFA
	setupRes, err := svc.SetupMFA(context.Background(), 201)
	if err != nil {
		t.Fatalf("SetupMFA failed: %v", err)
	}
	if setupRes.Secret == "" || !strings.HasPrefix(setupRes.OTPAuthURL, "otpauth://totp/") {
		t.Fatalf("unexpected setup response: %+v", setupRes)
	}

	// 2. Enable with invalid code should fail
	err = svc.EnableMFA(context.Background(), 201, "000000")
	if err == nil {
		t.Fatal("expected error for invalid code")
	}

	// 3. Enable with valid code
	validCode, _ := totp.GenerateCode(setupRes.Secret, time.Now().UTC())
	err = svc.EnableMFA(context.Background(), 201, validCode)
	if err != nil {
		t.Fatalf("EnableMFA failed: %v", err)
	}
	if !repo.usersByID[201].TOTPEnabled {
		t.Fatal("expected TOTPEnabled to be true in repository")
	}

	// 4. Disable with wrong password
	err = svc.DisableMFA(context.Background(), 201, "wrong-password", validCode)
	if err == nil {
		t.Fatal("expected error for wrong password")
	}

	// 5. Disable with correct password and code
	codeForDisable, _ := totp.GenerateCode(setupRes.Secret, time.Now().UTC())
	err = svc.DisableMFA(context.Background(), 201, "my-super-secret-pass-123", codeForDisable)
	if err != nil {
		t.Fatalf("DisableMFA failed: %v", err)
	}
	if repo.usersByID[201].TOTPEnabled {
		t.Fatal("expected TOTPEnabled to be false in repository")
	}
}

func TestAdminMandatoryMFASetupFlow(t *testing.T) {
	passwordHash, err := HashPassword("admin-super-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("01234567890123456789012345678901")

	adminUser := domain.User{
		ID:           301,
		Email:        "enforced-admin@example.com",
		PasswordHash: passwordHash,
		Role:         "super_admin",
		Status:       "active",
		TOTPEnabled:  false,
	}

	regularUser := domain.User{
		ID:           302,
		Email:        "regular-user@example.com",
		PasswordHash: passwordHash,
		Role:         "org_admin",
		Status:       "active",
		TOTPEnabled:  false,
	}

	repo := &mockAuthRepository{
		users: map[string]domain.User{
			"enforced-admin@example.com": adminUser,
			"regular-user@example.com":   regularUser,
		},
		usersByID: map[uint64]domain.User{
			301: adminUser,
			302: regularUser,
		},
	}

	svc := NewService(repo, secret, 15*time.Minute, 7*24*time.Hour).WithEnforceAdminMFA(true)

	// Both supported management roles are subject to the administrator MFA policy.
	userResp, err := svc.Login(context.Background(), "regular-user@example.com", "admin-super-pass-123")
	if err != nil {
		t.Fatalf("regular user login failed: %v", err)
	}
	if !userResp.MFARequired || !userResp.MFASetupRequired {
		t.Fatal("org admin should require MFA setup")
	}

	// 2. Admin user without MFA should be challenged with setup
	adminResp, err := svc.Login(context.Background(), "enforced-admin@example.com", "admin-super-pass-123")
	if err != nil {
		t.Fatalf("admin login failed: %v", err)
	}
	if !adminResp.MFARequired || !adminResp.MFASetupRequired {
		t.Fatal("expected MFARequired and MFASetupRequired to be true for admin")
	}
	if adminResp.Secret == "" || adminResp.OTPAuthURL == "" || adminResp.MFAToken == "" {
		t.Fatal("expected Secret, OTPAuthURL and MFAToken in setup response")
	}

	// 3. Verify setup with invalid code
	_, err = svc.VerifyMFA(context.Background(), adminResp.MFAToken, "999999")
	if err == nil {
		t.Fatal("expected verification failure on invalid code")
	}

	// 4. Verify setup with valid code
	now := time.Now().UTC()
	validCode, err := totp.GenerateCode(adminResp.Secret, now)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := svc.VerifyMFA(context.Background(), adminResp.MFAToken, validCode)
	if err != nil {
		t.Fatalf("VerifyMFA setup failed: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("expected access token after setup verification")
	}
	if !repo.usersByID[301].TOTPEnabled {
		t.Fatal("expected user TOTPEnabled to be persisted as true in repository")
	}

	// 5. Subsequent admin login now requires standard MFA (not setup)
	adminResp2, err := svc.Login(context.Background(), "enforced-admin@example.com", "admin-super-pass-123")
	if err != nil {
		t.Fatalf("subsequent admin login failed: %v", err)
	}
	if !adminResp2.MFARequired {
		t.Fatal("expected MFARequired to be true")
	}
	if adminResp2.MFASetupRequired {
		t.Fatal("expected MFASetupRequired to be false after user has bound TOTP")
	}
}
