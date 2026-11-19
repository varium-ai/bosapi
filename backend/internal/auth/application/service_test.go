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

type otpNotifierFunc func(context.Context, string, string) error

func (notify otpNotifierFunc) SendOTP(ctx context.Context, email, code string) error {
	return notify(ctx, email, code)
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
	var deliveredCode string
	notifier := otpNotifierFunc(func(_ context.Context, email, code string) error {
		if email != "user@example.com" {
			t.Fatalf("unexpected OTP recipient: %s", email)
		}
		deliveredCode = code
		return nil
	})
	svc := NewService(repo, secret, 15*time.Minute, 7*24*time.Hour, notifier)

	// 1. Request reset for non-existing email
	_, err = svc.RequestPasswordReset(context.Background(), "unknown@example.com")
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}

	// 2. Request reset for valid email; the notifier receives the code.
	expiresIn, err := svc.RequestPasswordReset(context.Background(), " USER@example.com ")
	if err != nil {
		t.Fatalf("unexpected error requesting reset: %v", err)
	}
	if expiresIn != 300 || len(deliveredCode) != 6 {
		t.Fatalf("expected 300s expiry and 6-digit delivered code, got expiry=%d code length=%d", expiresIn, len(deliveredCode))
	}

	// 3. Request immediately again - should be rate limited
	_, err = svc.RequestPasswordReset(context.Background(), "user@example.com")
	if err == nil {
		t.Fatal("expected rate limit error")
	}

	// 4. Reset with wrong OTP
	wrongCode := "000000"
	if deliveredCode == wrongCode {
		wrongCode = "111111"
	}
	err = svc.ResetPasswordWithOTP(context.Background(), "user@example.com", wrongCode, "new-secret-password-456")
	if err == nil {
		t.Fatal("expected error with wrong OTP")
	}

	// 5. Reset with invalid weak password
	err = svc.ResetPasswordWithOTP(context.Background(), "user@example.com", deliveredCode, "short")
	if err == nil {
		t.Fatal("expected error with weak password")
	}

	// 6. Reset with correct OTP and valid password
	err = svc.ResetPasswordWithOTP(context.Background(), "user@example.com", deliveredCode, "new-secret-password-456")
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
	err = svc.ResetPasswordWithOTP(context.Background(), "user@example.com", deliveredCode, "another-valid-password-789")
	if err == nil {
		t.Fatal("expected error reusing OTP")
	}
}

func TestPasswordResetWithoutNotifierFailsClosed(t *testing.T) {
	user := domain.User{ID: 42, Email: "user@example.com", Status: "active"}
	repo := &mockAuthRepository{users: map[string]domain.User{user.Email: user}}
	for _, notifiers := range [][]OTPNotifier{nil, {nil}} {
		svc := NewService(repo, []byte("01234567890123456789012345678901"), time.Minute, time.Hour, notifiers...)
		expiresIn, err := svc.RequestPasswordReset(context.Background(), user.Email)
		if !errors.Is(err, ErrPasswordResetUnavailable) || expiresIn != 0 {
			t.Fatalf("expected unavailable reset, got expiry=%d err=%v", expiresIn, err)
		}
		if len(svc.challenges) != 0 {
			t.Fatal("missing notifier must not create a challenge")
		}
	}
}

func TestPasswordResetDeliveryFailureAllowsImmediateRetry(t *testing.T) {
	user := domain.User{ID: 42, Email: "user@example.com", Status: "active"}
	repo := &mockAuthRepository{users: map[string]domain.User{user.Email: user}}
	var code string
	deliveryErr := errors.New("SMTP unavailable")
	svc := NewService(repo, []byte("01234567890123456789012345678901"), time.Minute, time.Hour,
		otpNotifierFunc(func(_ context.Context, _ string, sentCode string) error {
			code = sentCode
			return deliveryErr
		}))
	expiresIn, err := svc.RequestPasswordReset(context.Background(), user.Email)
	if !errors.Is(err, ErrPasswordResetUnavailable) || expiresIn != 0 {
		t.Fatalf("expected delivery failure, got expiry=%d err=%v", expiresIn, err)
	}
	if len(svc.challenges) != 0 {
		t.Fatal("failed delivery must remove its challenge")
	}
	if err := svc.ResetPasswordWithOTP(context.Background(), user.Email, code, "new-secret-password-456"); err == nil {
		t.Fatal("failed delivery code must not authorize a password reset")
	}
	deliveryErr = nil
	if expiresIn, err := svc.RequestPasswordReset(context.Background(), user.Email); err != nil || expiresIn != 300 {
		t.Fatalf("failed delivery must permit immediate retry: expiry=%d err=%v", expiresIn, err)
	}
}

func TestPasswordResetLateDeliveryFailurePreservesNewerChallenge(t *testing.T) {
	user := domain.User{ID: 42, Email: "user@example.com", Status: "active"}
	repo := &mockAuthRepository{
		users:       map[string]domain.User{user.Email: user},
		usersByID:   map[uint64]domain.User{user.ID: user},
		updatedPass: make(map[uint64]string),
	}
	firstCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 1)
	var newerCode string
	svc := NewService(repo, []byte("01234567890123456789012345678901"), time.Minute, time.Hour,
		otpNotifierFunc(func(ctx context.Context, _ string, code string) error {
			if ctx == firstCtx {
				started <- code
				<-ctx.Done()
				return ctx.Err()
			}
			newerCode = code
			return nil
		}))
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }
	result := make(chan error, 1)
	go func() {
		_, err := svc.RequestPasswordReset(firstCtx, user.Email)
		result <- err
	}()
	select {
	case code := <-started:
		if err := svc.ResetPasswordWithOTP(context.Background(), user.Email, code, "new-secret-password-456"); err == nil {
			t.Fatal("code must not authorize a reset before delivery succeeds")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first delivery did not start")
	}

	now = now.Add(31 * time.Second)
	if _, err := svc.RequestPasswordReset(context.Background(), user.Email); err != nil {
		t.Fatalf("newer delivery failed: %v", err)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrPasswordResetUnavailable) {
			t.Fatalf("expected first delivery to fail, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first delivery did not finish")
	}
	if err := svc.ResetPasswordWithOTP(context.Background(), user.Email, newerCode, "new-secret-password-456"); err != nil {
		t.Fatalf("old delivery failure invalidated the newer code: %v", err)
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
