package application

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"
	"time"

	"bosapi/backend/internal/auth/domain"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/totp"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	ErrInactiveUser        = errors.New("user is inactive")
	ErrLoginLocked         = errors.New("login is temporarily locked")
	ErrInvalidMFA          = errors.New("invalid verification code")
	ErrMFANotEnabled       = errors.New("two-factor authentication is not enabled")
	ErrMFAAlreadyEnabled   = errors.New("two-factor authentication is already enabled")
	ErrMFASetupExpired     = errors.New("mfa setup session expired")
)

// Cipher defines authenticated encryption for TOTP secrets.
type Cipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(encoded string) (string, error)
}

type defaultCipher struct {
	aead cipher.AEAD
}

func newDefaultCipher(key []byte) (*defaultCipher, error) {
	if len(key) < 32 {
		return nil, errors.New("key must be at least 32 bytes")
	}
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &defaultCipher{aead: aead}, nil
}

func (c *defaultCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte("bosapi/totp/v1"))
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *defaultCipher) Decrypt(encoded string) (string, error) {
	sealed, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < c.aead.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	nonce, payload := sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, payload, []byte("bosapi/totp/v1"))
	if err != nil {
		return "", errors.New("failed to decrypt ciphertext")
	}
	return string(plaintext), nil
}

// OTPNotifier defines delivery of verification codes.
type OTPNotifier interface {
	SendOTP(ctx context.Context, email, code string) error
}

type resetChallenge struct {
	email     string
	userID    uint64
	codeHash  string
	attempts  int
	expiresAt time.Time
	createdAt time.Time
}

type pendingMFASetup struct {
	secret    string
	expiresAt time.Time
}

// MFASetupResult provides manual secret and QR URI.
type MFASetupResult struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
	ExpiresIn  int64  `json:"expires_in"`
}

// Repository defines persistence operations used by the authentication service.
type Repository interface {
	FindByEmail(context.Context, string) (domain.User, error)
	FindByID(context.Context, uint64) (domain.User, error)
	FindRefreshUser(context.Context, string, time.Time) (domain.User, error)
	CreateRefresh(context.Context, *domain.RefreshSession) error
	RotateRefresh(context.Context, string, *domain.RefreshSession, time.Time) error
	RevokeRefresh(context.Context, string, time.Time) error
	RevokeAccess(context.Context, string, time.Time) error
	IsAccessRevoked(context.Context, string) (bool, error)
	UpdatePassword(context.Context, uint64, string) error
	RevokeAllUserRefresh(context.Context, uint64, time.Time) error
	EnableTOTP(ctx context.Context, userID uint64, encryptedSecret string, enabledAt time.Time) error
	DisableTOTP(ctx context.Context, userID uint64) error
	RecordLoginAttempt(context.Context, string, string, bool, string, time.Time) error
}

// RecordLoginAttempt stores a successful or failed credential attempt without blocking authentication.
func (service *Service) RecordLoginAttempt(ctx context.Context, email, ip string, success bool, reason string, at time.Time) error {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if len(normalized) > 320 || len(ip) > 45 {
		return errors.New("invalid login audit record")
	}
	return service.repository.RecordLoginAttempt(ctx, normalized, ip, success, reason, at.UTC())
}

// Service implements portal login and revocable access/rotating refresh tokens.
type Service struct {
	repository      Repository
	secret          []byte
	accessTTL       time.Duration
	refreshTTL      time.Duration
	now             func() time.Time
	notifier        OTPNotifier
	challenges      map[string]resetChallenge
	cipher          Cipher
	pendingMFA      map[uint64]pendingMFASetup
	usedMFASteps    map[string]time.Time
	enforceAdminMFA bool
	loginLock       domain.LoginLockStore
	mu              sync.Mutex
}

// TokenPair contains the short-lived access JWT and one-time refresh token.
type TokenPair struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	TokenType    string          `json:"token_type"`
	ExpiresIn    int64           `json:"expires_in"`
	User         domain.Identity `json:"user"`
}

// LoginResponse contains either full tokens or an MFA challenge.
type LoginResponse struct {
	TokenPair
	MFARequired      bool   `json:"mfa_required"`
	MFASetupRequired bool   `json:"mfa_setup_required,omitempty"`
	MFAToken         string `json:"mfa_token,omitempty"`
	Secret           string `json:"secret,omitempty"`
	OTPAuthURL       string `json:"otpauth_url,omitempty"`
}

// NewService constructs authentication use cases with explicit signing and expiry policy.
func NewService(repository Repository, secret []byte, accessTTL, refreshTTL time.Duration, notifiers ...OTPNotifier) *Service {
	var notifier OTPNotifier
	if len(notifiers) > 0 {
		notifier = notifiers[0]
	}
	var defCipher Cipher
	if len(secret) >= 32 {
		defCipher, _ = newDefaultCipher(secret)
	}
	return &Service{
		repository:   repository,
		secret:       append([]byte(nil), secret...),
		accessTTL:    accessTTL,
		refreshTTL:   refreshTTL,
		now:          time.Now,
		notifier:     notifier,
		challenges:   make(map[string]resetChallenge),
		cipher:       defCipher,
		pendingMFA:   make(map[uint64]pendingMFASetup),
		usedMFASteps: make(map[string]time.Time),
	}
}

// WithCipher configures custom encryption cipher for TOTP secrets.
func (service *Service) WithCipher(c Cipher) *Service {
	if c != nil {
		service.cipher = c
	}
	return service
}

// WithEnforceAdminMFA configures whether administrative accounts must undergo MFA upon login.
func (service *Service) WithEnforceAdminMFA(enforce bool) *Service {
	service.enforceAdminMFA = enforce
	return service
}

// WithLoginLock configures shared temporary account lockouts. A nil store keeps
// the legacy behavior and is used only when shared lock state is unavailable.
func (service *Service) WithLoginLock(store domain.LoginLockStore) *Service {
	service.loginLock = store
	return service
}

// Login verifies Argon2id credentials and issues a fresh token pair or MFA challenge.
func (service *Service) Login(ctx context.Context, email, password string) (LoginResponse, error) {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	if err := service.checkLoginLock(ctx, normalizedEmail); err != nil {
		return LoginResponse{}, err
	}
	user, err := service.repository.FindByEmail(ctx, normalizedEmail)
	if err != nil || !VerifyPassword(user.PasswordHash, password) {
		service.recordLoginFailure(ctx, normalizedEmail)
		return LoginResponse{}, ErrInvalidCredentials
	}
	if user.Status != "active" {
		service.recordLoginFailure(ctx, normalizedEmail)
		return LoginResponse{}, ErrInactiveUser
	}
	isAdmin := user.Role == "super_admin" || user.Role == "org_admin"
	if user.TOTPEnabled && user.TOTPSecretEncrypted != nil && *user.TOTPSecretEncrypted != "" {
		mfaToken, err := service.issueMFAToken(user, false)
		if err != nil {
			return LoginResponse{}, err
		}
		return LoginResponse{
			MFARequired: true,
			MFAToken:    mfaToken,
		}, nil
	} else if service.enforceAdminMFA && isAdmin {
		secret, err := totp.GenerateSecret()
		if err != nil {
			return LoginResponse{}, fmt.Errorf("generate totp secret: %w", err)
		}
		uri := totp.BuildURI("BOS API", user.Email, secret)
		now := service.now().UTC()
		service.mu.Lock()
		service.pendingMFA[user.ID] = pendingMFASetup{
			secret:    secret,
			expiresAt: now.Add(10 * time.Minute),
		}
		service.mu.Unlock()

		mfaToken, err := service.issueMFAToken(user, true)
		if err != nil {
			return LoginResponse{}, err
		}
		return LoginResponse{
			MFARequired:      true,
			MFASetupRequired: true,
			MFAToken:         mfaToken,
			Secret:           secret,
			OTPAuthURL:       uri,
		}, nil
	}
	pair, err := service.issue(ctx, user)
	if err != nil {
		return LoginResponse{}, err
	}
	service.unlockLogin(ctx, normalizedEmail)
	return LoginResponse{
		TokenPair: pair,
	}, nil
}

func (service *Service) checkLoginLock(ctx context.Context, email string) error {
	if service.loginLock == nil {
		return nil
	}
	decision, err := service.loginLock.Check(ctx, email, service.now().UTC())
	if err == nil && decision.Locked {
		return ErrLoginLocked
	}
	return nil
}

func (service *Service) recordLoginFailure(ctx context.Context, email string) {
	if service.loginLock == nil || email == "" {
		return
	}
	_, _ = service.loginLock.RecordFailure(ctx, email, service.now().UTC())
}

func (service *Service) unlockLogin(ctx context.Context, email string) {
	if service.loginLock == nil || email == "" {
		return
	}
	_ = service.loginLock.Unlock(ctx, email)
}

// Refresh atomically consumes the prior refresh token and issues a replacement.
func (service *Service) Refresh(ctx context.Context, raw string) (TokenPair, error) {
	if len(raw) < 32 || len(raw) > 256 {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	now := service.now().UTC()
	user, err := service.repository.FindRefreshUser(ctx, tokenHash(raw), now)
	if err != nil || user.Status != "active" {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	newRaw, session, err := service.newRefresh(user.ID, now)
	if err != nil {
		return TokenPair{}, err
	}
	if err := service.repository.RotateRefresh(ctx, tokenHash(raw), session, now); err != nil {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	return service.pair(user, newRaw)
}

// Logout revokes both the current access token and its refresh credential.
func (service *Service) Logout(ctx context.Context, jti, refresh string, accessExpiry time.Time) error {
	now := service.now().UTC()
	if jti != "" && accessExpiry.After(now) {
		if err := service.repository.RevokeAccess(ctx, jti, accessExpiry); err != nil {
			return fmt.Errorf("revoke access token: %w", err)
		}
	}
	if refresh != "" {
		if err := service.repository.RevokeRefresh(ctx, tokenHash(refresh), now); err != nil {
			return fmt.Errorf("revoke refresh token: %w", err)
		}
	}
	return nil
}

// VerifyAccess validates a token's signature, expiry, revocation status, and identity claims.
func (service *Service) VerifyAccess(ctx context.Context, encoded string) (domain.User, string, time.Time, error) {
	token, err := jwt.Parse(encoded, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("invalid signing method")
		}
		return service.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return domain.User{}, "", time.Time{}, ErrInvalidCredentials
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return domain.User{}, "", time.Time{}, ErrInvalidCredentials
	}
	jti, jtiOK := claims["jti"].(string)
	userID, userOK := numericClaim(claims["sub"])
	organizationID, organizationOK := numericClaim(claims["organization_id"])
	role, roleOK := claims["role"].(string)
	if role == "super_admin" && claims["organization_id"] == float64(0) {
		organizationID, organizationOK = 0, true
	}
	expiration, expirationErr := claims.GetExpirationTime()
	expOK := expirationErr == nil && expiration != nil
	if !jtiOK || jti == "" || !userOK || !organizationOK || !roleOK || !expOK {
		return domain.User{}, "", time.Time{}, ErrInvalidCredentials
	}
	revoked, err := service.repository.IsAccessRevoked(ctx, jti)
	if err != nil || revoked {
		return domain.User{}, "", time.Time{}, ErrInvalidCredentials
	}
	user, err := service.repository.FindByID(ctx, userID)
	if err != nil || user.Status != "active" || user.OrganizationID != organizationID || user.Role != role || role != "super_admin" && organizationID == 0 {
		return domain.User{}, "", time.Time{}, ErrInvalidCredentials
	}
	return user, jti, expiration.Time, nil
}

// HashPassword creates an Argon2id password encoding with a random salt.
func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 128 {
		return "", errors.New("password must contain 12 to 128 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return "$argon2id$v=19$m=65536,t=3,p=2$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}

// VerifyPassword checks an encoded Argon2id password using a constant-time comparison.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=2" {
		return false
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(parts[4])
	expected, hashErr := base64.RawStdEncoding.DecodeString(parts[5])
	if saltErr != nil || hashErr != nil || len(salt) != 16 || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func (service *Service) issue(ctx context.Context, user domain.User) (TokenPair, error) {
	now := service.now().UTC()
	raw, session, err := service.newRefresh(user.ID, now)
	if err != nil {
		return TokenPair{}, err
	}
	if err := service.repository.CreateRefresh(ctx, session); err != nil {
		return TokenPair{}, fmt.Errorf("save refresh token: %w", err)
	}
	return service.pair(user, raw)
}

func (service *Service) newRefresh(userID uint64, now time.Time) (string, *domain.RefreshSession, error) {
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}
	raw := "refresh_" + base64.RawURLEncoding.EncodeToString(entropy)
	return raw, &domain.RefreshSession{UserID: userID, TokenHash: tokenHash(raw), ExpiresAt: now.Add(service.refreshTTL)}, nil
}

func (service *Service) pair(user domain.User, refresh string) (TokenPair, error) {
	now := service.now().UTC()
	var jtiBytes [16]byte
	if _, err := rand.Read(jtiBytes[:]); err != nil {
		return TokenPair{}, fmt.Errorf("generate token identifier: %w", err)
	}
	claims := jwt.MapClaims{"sub": user.ID, "organization_id": user.OrganizationID, "role": user.Role, "email": user.Email, "iat": now.Unix(), "exp": now.Add(service.accessTTL).Unix(), "jti": hex.EncodeToString(jtiBytes[:])}
	encoded, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(service.secret)
	if err != nil {
		return TokenPair{}, fmt.Errorf("sign access token: %w", err)
	}
	return TokenPair{AccessToken: encoded, RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: int64(service.accessTTL.Seconds()), User: user.PublicIdentity()}, nil
}

func tokenHash(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func numericClaim(value any) (uint64, bool) {
	switch number := value.(type) {
	case float64:
		if number < 1 || number != float64(uint64(number)) {
			return 0, false
		}
		return uint64(number), true
	case json.Number:
		parsed, err := number.Int64()
		return uint64(parsed), err == nil && parsed > 0
	default:
		return 0, false
	}
}

// ChangePassword verifies current password and updates to a new Argon2id password.
func (service *Service) ChangePassword(ctx context.Context, userID uint64, currentPassword, newPassword string) error {
	if len(newPassword) < 12 || len(newPassword) > 128 {
		return errors.New("new password must contain 12 to 128 characters")
	}
	user, err := service.repository.FindByID(ctx, userID)
	if err != nil {
		return ErrInvalidCredentials
	}
	if !VerifyPassword(user.PasswordHash, currentPassword) {
		return errors.New("current password is incorrect")
	}
	if currentPassword == newPassword {
		return errors.New("new password cannot be the same as current password")
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := service.repository.UpdatePassword(ctx, userID, hash); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	_ = service.repository.RevokeAllUserRefresh(ctx, userID, service.now().UTC())
	return nil
}

// RequestPasswordReset creates a 6-digit challenge code and dispatches it via notifier or console log.
func (service *Service) RequestPasswordReset(ctx context.Context, email string) (int, string, error) {
	normEmail := strings.ToLower(strings.TrimSpace(email))
	if normEmail == "" || len(normEmail) > 320 || !strings.Contains(normEmail, "@") {
		return 0, "", errors.New("please provide a valid email address")
	}

	user, err := service.repository.FindByEmail(ctx, normEmail)
	if err != nil {
		return 0, "", errors.New("user with this email does not exist")
	}
	if user.Status != "active" {
		return 0, "", errors.New("user account is inactive or disabled")
	}

	now := service.now().UTC()
	service.mu.Lock()
	if existing, found := service.challenges[normEmail]; found && now.Before(existing.expiresAt) {
		if now.Sub(existing.createdAt) < 30*time.Second {
			service.mu.Unlock()
			return 0, "", errors.New("verification code was recently requested; please wait 30 seconds")
		}
	}

	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		service.mu.Unlock()
		return 0, "", fmt.Errorf("generate verification code: %w", err)
	}
	code := fmt.Sprintf("%06d", number.Int64())
	hash := service.codeHash(normEmail, code)
	service.challenges[normEmail] = resetChallenge{
		email:     normEmail,
		userID:    user.ID,
		codeHash:  hash,
		attempts:  0,
		expiresAt: now.Add(5 * time.Minute),
		createdAt: now,
	}
	service.mu.Unlock()

	var devCode string
	if service.notifier != nil {
		_ = service.notifier.SendOTP(ctx, normEmail, code)
	} else {
		devCode = code
	}
	logging.Event(ctx, "INFO", "auth.password_reset.requested", "verification code generated")

	return 300, devCode, nil
}

// ResetPasswordWithOTP verifies the 6-digit challenge code and updates the user's password.
func (service *Service) ResetPasswordWithOTP(ctx context.Context, email, otp, newPassword string) error {
	normEmail := strings.ToLower(strings.TrimSpace(email))
	otp = strings.TrimSpace(otp)
	if len(otp) != 6 {
		return errors.New("verification code must be 6 digits")
	}
	if len(newPassword) < 12 || len(newPassword) > 128 {
		return errors.New("password must contain 12 to 128 characters")
	}

	now := service.now().UTC()
	service.mu.Lock()
	challenge, found := service.challenges[normEmail]
	if !found || now.After(challenge.expiresAt) {
		delete(service.challenges, normEmail)
		service.mu.Unlock()
		return errors.New("verification code has expired or was not requested; please request a new code")
	}

	if challenge.attempts >= 5 {
		delete(service.challenges, normEmail)
		service.mu.Unlock()
		return errors.New("too many failed verification attempts; please request a new code")
	}

	expectedHash := service.codeHash(normEmail, otp)
	if subtle.ConstantTimeCompare([]byte(challenge.codeHash), []byte(expectedHash)) != 1 {
		challenge.attempts++
		service.challenges[normEmail] = challenge
		service.mu.Unlock()
		return errors.New("invalid verification code")
	}

	delete(service.challenges, normEmail)
	userID := challenge.userID
	service.mu.Unlock()

	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}

	if err := service.repository.UpdatePassword(ctx, userID, hash); err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	_ = service.repository.RevokeAllUserRefresh(ctx, userID, now)
	return nil
}

func (service *Service) codeHash(email, code string) string {
	mac := hmac.New(sha256.New, service.secret)
	_, _ = fmt.Fprintf(mac, "%s\x00%s", strings.ToLower(email), code)
	return hex.EncodeToString(mac.Sum(nil))
}

func (service *Service) issueMFAToken(user domain.User, isSetup bool) (string, error) {
	now := service.now().UTC()
	var jtiBytes [16]byte
	if _, err := rand.Read(jtiBytes[:]); err != nil {
		return "", fmt.Errorf("generate mfa token id: %w", err)
	}
	claims := jwt.MapClaims{
		"sub":      user.ID,
		"scope":    "mfa_pending",
		"email":    user.Email,
		"is_setup": isSetup,
		"iat":      now.Unix(),
		"exp":      now.Add(10 * time.Minute).Unix(),
		"jti":      hex.EncodeToString(jtiBytes[:]),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(service.secret)
}

// VerifyMFA validates the 6-digit TOTP code and temporary MFA token, issuing a full token pair.
func (service *Service) VerifyMFA(ctx context.Context, mfaToken, code string) (TokenPair, error) {
	if strings.TrimSpace(mfaToken) == "" || strings.TrimSpace(code) == "" {
		return TokenPair{}, ErrInvalidMFA
	}
	token, err := jwt.Parse(mfaToken, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("invalid signing method")
		}
		return service.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return TokenPair{}, ErrInvalidMFA
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return TokenPair{}, ErrInvalidMFA
	}
	scope, _ := claims["scope"].(string)
	if scope != "mfa_pending" {
		return TokenPair{}, ErrInvalidMFA
	}
	userID, ok := numericClaim(claims["sub"])
	if !ok {
		return TokenPair{}, ErrInvalidMFA
	}

	user, err := service.repository.FindByID(ctx, userID)
	if err != nil || user.Status != "active" {
		return TokenPair{}, ErrInvalidCredentials
	}

	isSetup, _ := claims["is_setup"].(bool)
	now := service.now().UTC()
	var secret string

	if isSetup {
		service.mu.Lock()
		pending, found := service.pendingMFA[userID]
		if !found || now.After(pending.expiresAt) {
			delete(service.pendingMFA, userID)
			service.mu.Unlock()
			return TokenPair{}, ErrMFASetupExpired
		}
		secret = pending.secret
		service.mu.Unlock()
	} else {
		if !user.TOTPEnabled || user.TOTPSecretEncrypted == nil {
			return TokenPair{}, ErrInvalidCredentials
		}
		if service.cipher == nil {
			return TokenPair{}, errors.New("cipher not configured")
		}
		var err error
		secret, err = service.cipher.Decrypt(*user.TOTPSecretEncrypted)
		if err != nil {
			return TokenPair{}, fmt.Errorf("decrypt totp secret: %w", err)
		}
	}

	if !totp.Validate(code, secret, now, 1) {
		return TokenPair{}, ErrInvalidMFA
	}

	step := now.Unix() / 30
	replayKey := fmt.Sprintf("%d:%d", user.ID, step)
	service.mu.Lock()
	if exp, exists := service.usedMFASteps[replayKey]; exists && now.Before(exp) {
		service.mu.Unlock()
		return TokenPair{}, errors.New("code has already been used")
	}
	service.usedMFASteps[replayKey] = now.Add(90 * time.Second)
	if len(service.usedMFASteps) > 1000 {
		for k, v := range service.usedMFASteps {
			if now.After(v) {
				delete(service.usedMFASteps, k)
			}
		}
	}
	service.mu.Unlock()

	if isSetup {
		if service.cipher == nil {
			return TokenPair{}, errors.New("cipher not configured")
		}
		encrypted, err := service.cipher.Encrypt(secret)
		if err != nil {
			return TokenPair{}, fmt.Errorf("encrypt totp secret: %w", err)
		}
		if err := service.repository.EnableTOTP(ctx, user.ID, encrypted, now); err != nil {
			return TokenPair{}, fmt.Errorf("enable totp: %w", err)
		}
		service.mu.Lock()
		delete(service.pendingMFA, user.ID)
		service.mu.Unlock()
		user.TOTPEnabled = true
		user.TOTPSecretEncrypted = &encrypted
	}

	pair, err := service.issue(ctx, user)
	if err != nil {
		return TokenPair{}, err
	}
	service.unlockLogin(ctx, user.Email)
	return pair, nil
}

// SetupMFA generates a new TOTP secret and QR URI for the user to bind.
func (service *Service) SetupMFA(ctx context.Context, userID uint64) (MFASetupResult, error) {
	user, err := service.repository.FindByID(ctx, userID)
	if err != nil || user.Status != "active" {
		return MFASetupResult{}, ErrInvalidCredentials
	}
	if user.TOTPEnabled {
		return MFASetupResult{}, ErrMFAAlreadyEnabled
	}

	secret, err := totp.GenerateSecret()
	if err != nil {
		return MFASetupResult{}, fmt.Errorf("generate totp secret: %w", err)
	}
	uri := totp.BuildURI("BOS API", user.Email, secret)

	now := service.now().UTC()
	service.mu.Lock()
	service.pendingMFA[userID] = pendingMFASetup{
		secret:    secret,
		expiresAt: now.Add(10 * time.Minute),
	}
	service.mu.Unlock()

	return MFASetupResult{
		Secret:     secret,
		OTPAuthURL: uri,
		ExpiresIn:  600,
	}, nil
}

// EnableMFA confirms the pending secret with an authenticator code and enables MFA.
func (service *Service) EnableMFA(ctx context.Context, userID uint64, code string) error {
	user, err := service.repository.FindByID(ctx, userID)
	if err != nil || user.Status != "active" {
		return ErrInvalidCredentials
	}
	if user.TOTPEnabled {
		return ErrMFAAlreadyEnabled
	}

	now := service.now().UTC()
	service.mu.Lock()
	pending, found := service.pendingMFA[userID]
	if !found || now.After(pending.expiresAt) {
		delete(service.pendingMFA, userID)
		service.mu.Unlock()
		return ErrMFASetupExpired
	}
	service.mu.Unlock()

	if !totp.Validate(code, pending.secret, now, 1) {
		return ErrInvalidMFA
	}

	if service.cipher == nil {
		return errors.New("cipher not configured")
	}

	encrypted, err := service.cipher.Encrypt(pending.secret)
	if err != nil {
		return fmt.Errorf("encrypt totp secret: %w", err)
	}

	if err := service.repository.EnableTOTP(ctx, userID, encrypted, now); err != nil {
		return fmt.Errorf("enable totp: %w", err)
	}

	service.mu.Lock()
	delete(service.pendingMFA, userID)
	service.mu.Unlock()

	return nil
}

// DisableMFA verifies the current password and TOTP code before disabling MFA.
func (service *Service) DisableMFA(ctx context.Context, userID uint64, currentPassword, code string) error {
	user, err := service.repository.FindByID(ctx, userID)
	if err != nil || user.Status != "active" {
		return ErrInvalidCredentials
	}
	if !user.TOTPEnabled || user.TOTPSecretEncrypted == nil {
		return ErrMFANotEnabled
	}

	if !VerifyPassword(user.PasswordHash, currentPassword) {
		return errors.New("current password is incorrect")
	}

	if service.cipher == nil {
		return errors.New("cipher not configured")
	}

	secret, err := service.cipher.Decrypt(*user.TOTPSecretEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt totp secret: %w", err)
	}

	now := service.now().UTC()
	if !totp.Validate(code, secret, now, 1) {
		return ErrInvalidMFA
	}

	if err := service.repository.DisableTOTP(ctx, userID); err != nil {
		return fmt.Errorf("disable totp: %w", err)
	}

	return nil
}
