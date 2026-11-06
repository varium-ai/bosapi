package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"

	"bosapi/backend/internal/admin/domain"
	analyticsdomain "bosapi/backend/internal/analytics/domain"
	"bosapi/backend/internal/platform/edition"
)

const SettingKeyGatewayEndpoints = "gateway.endpoints"
const SettingKeyMessageNotifications = "notifications.message"

var gatewayEndpointPattern = regexp.MustCompile(`^[a-zA-Z0-9._~:/\[\]-]+$`)

// SystemSettingService provides operations for managing system settings and platform configurations.
type SystemSettingService struct {
	repo domain.SystemSettingRepository
}

// NewSystemSettingService constructs a new SystemSettingService.
func NewSystemSettingService(repo domain.SystemSettingRepository) *SystemSettingService {
	return &SystemSettingService{repo: repo}
}

// GetGatewayEndpoint returns the saved API address, or an empty unconfigured value.
func (s *SystemSettingService) GetGatewayEndpoint(ctx context.Context) (domain.GatewayEndpoint, error) {
	var config domain.GatewayEndpoint
	setting, err := s.repo.Get(ctx, SettingKeyGatewayEndpoints)
	if err != nil {
		return config, fmt.Errorf("read gateway endpoint setting: %w", err)
	}
	if setting == nil || strings.TrimSpace(setting.ValueText) == "" {
		return config, nil
	}

	// Retain the explicitly saved active address from older installations.
	// The next save writes only endpoint; no environment defaults are inferred.
	var parsed struct {
		Endpoint          *string `json:"endpoint"`
		ProdEndpoint      string  `json:"prod_endpoint"`
		TestEndpoint      string  `json:"test_endpoint"`
		ActiveEnvironment string  `json:"active_environment"`
	}
	if err := json.Unmarshal([]byte(setting.ValueText), &parsed); err != nil {
		return config, nil
	}
	var endpoint string
	if parsed.Endpoint != nil {
		endpoint = *parsed.Endpoint
	} else {
		switch parsed.ActiveEnvironment {
		case "prod":
			endpoint = parsed.ProdEndpoint
		case "", "test":
			endpoint = parsed.TestEndpoint
		}
	}
	endpoint = sanitizeEndpoint(endpoint)
	if validateEndpoint(endpoint) == nil {
		config.Endpoint = endpoint
	}
	return config, nil
}

// UpdateGatewayEndpoint validates and persists this installation's API address.
func (s *SystemSettingService) UpdateGatewayEndpoint(ctx context.Context, config domain.GatewayEndpoint, updatedBy *uint64) (domain.GatewayEndpoint, error) {
	endpoint := sanitizeEndpoint(config.Endpoint)
	if err := validateEndpoint(endpoint); err != nil {
		return domain.GatewayEndpoint{}, fmt.Errorf("invalid gateway endpoint: %w", err)
	}

	persisted := domain.GatewayEndpoint{Endpoint: endpoint}
	bytes, err := json.Marshal(persisted)
	if err != nil {
		return domain.GatewayEndpoint{}, fmt.Errorf("marshal gateway endpoint: %w", err)
	}

	if err := s.repo.Set(ctx, SettingKeyGatewayEndpoints, string(bytes), false, updatedBy); err != nil {
		return domain.GatewayEndpoint{}, fmt.Errorf("save gateway endpoint setting: %w", err)
	}

	return persisted, nil
}

// ClearGatewayEndpoint leaves this installation unconfigured.
func (s *SystemSettingService) ClearGatewayEndpoint(ctx context.Context) (domain.GatewayEndpoint, error) {
	if err := s.repo.Delete(ctx, SettingKeyGatewayEndpoints); err != nil {
		return domain.GatewayEndpoint{}, fmt.Errorf("delete gateway endpoint setting: %w", err)
	}
	return domain.GatewayEndpoint{}, nil
}

// GetPublicPlatformConfig returns platform configurations accessible to all users.
func (s *SystemSettingService) GetPublicPlatformConfig(ctx context.Context) (map[string]any, error) {
	config, err := s.GetGatewayEndpoint(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"gateway_endpoint": config.Endpoint,
	}, nil
}

// Generic settings helpers for future extensible settings

func (s *SystemSettingService) GetSetting(ctx context.Context, key string) (*domain.SystemSetting, error) {
	if edition.ReservedSetting(key) {
		return nil, nil
	}
	return s.repo.Get(ctx, key)
}

func (s *SystemSettingService) SetSetting(ctx context.Context, key string, valueText string, isSecret bool, updatedBy *uint64) error {
	if edition.ReservedSetting(key) {
		return errors.New("setting is managed internally")
	}
	if analyticsdomain.ReservedSetting(key) {
		return errors.New("use the usage-reporting endpoint")
	}
	return s.repo.Set(ctx, key, valueText, isSecret, updatedBy)
}

func (s *SystemSettingService) DeleteSetting(ctx context.Context, key string) error {
	if edition.ReservedSetting(key) {
		return errors.New("setting is managed internally")
	}
	if analyticsdomain.ReservedSetting(key) {
		return errors.New("use the usage-reporting endpoint")
	}
	return s.repo.Delete(ctx, key)
}

func (s *SystemSettingService) ListSettings(ctx context.Context, prefix string) ([]domain.SystemSetting, error) {
	settings, err := s.repo.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	visible := make([]domain.SystemSetting, 0, len(settings))
	for _, setting := range settings {
		if !edition.ReservedSetting(setting.SettingKey) {
			visible = append(visible, setting)
		}
	}
	return visible, nil
}

func (s *SystemSettingService) GetMessageNotificationConfig(ctx context.Context) (domain.MessageNotificationConfig, error) {
	setting, err := s.repo.Get(ctx, SettingKeyMessageNotifications)
	if err != nil || setting == nil || strings.TrimSpace(setting.ValueText) == "" {
		return domain.MessageNotificationConfig{Provider: "lark"}, err
	}
	var config domain.MessageNotificationConfig
	if err := json.Unmarshal([]byte(setting.ValueText), &config); err != nil {
		return domain.MessageNotificationConfig{}, fmt.Errorf("decode message notification settings: %w", err)
	}
	if config.Provider == "" {
		config.Provider = "lark"
	}
	return config, nil
}

func (s *SystemSettingService) UpdateMessageNotificationConfig(ctx context.Context, config domain.MessageNotificationConfig, updatedBy *uint64) error {
	previous, err := s.GetMessageNotificationConfig(ctx)
	if err != nil {
		return err
	}
	if config.LarkWebhook == "" {
		config.LarkWebhook = previous.LarkWebhook
	}
	config.WebhookURL = strings.TrimSpace(config.WebhookURL)
	if config.WebhookURL == "" {
		config.WebhookURL = previous.WebhookURL
	}
	if config.SMTPPassword == "" {
		config.SMTPPassword = previous.SMTPPassword
	}
	if config.Provider == "lark" || config.Provider == "webhook" {
		config.AlertEmails = nil
	}
	if err := validateMessageNotificationConfig(config); err != nil {
		return err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode message notification settings: %w", err)
	}
	if err := s.repo.Set(ctx, SettingKeyMessageNotifications, string(encoded), true, updatedBy); err != nil {
		return fmt.Errorf("persist message notification settings: %w", err)
	}
	return nil
}

func validateMessageNotificationConfig(config domain.MessageNotificationConfig) error {
	if config.Provider != "lark" && config.Provider != "email" && config.Provider != "webhook" {
		return errors.New("provider must be lark, email or webhook")
	}
	if config.Provider == "lark" {
		if strings.TrimSpace(config.LarkWebhook) == "" {
			return errors.New("lark bot URL is required")
		}
		if err := validateLarkWebhook(config.LarkWebhook); err != nil {
			return err
		}
	}
	if config.Provider == "webhook" {
		if err := domain.ValidateMessageWebhookURL(config.WebhookURL); err != nil {
			return err
		}
	}
	if config.Provider == "email" {
		if strings.TrimSpace(config.SMTPHost) == "" || config.SMTPPort < 1 || config.SMTPPort > 65535 {
			return errors.New("valid SMTP host and port are required")
		}
		if strings.ContainsAny(config.SMTPHost, " \t\r\n/@") || len(config.SMTPHost) > 253 {
			return errors.New("SMTP host is invalid")
		}
		if _, err := mail.ParseAddress(config.FromEmail); err != nil {
			return errors.New("valid sender email is required")
		}
		if len(config.AlertEmails) > 100 {
			return errors.New("alert email list cannot exceed 100 addresses")
		}
		for _, address := range config.AlertEmails {
			if _, err := mail.ParseAddress(strings.TrimSpace(address)); err != nil {
				return errors.New("alert email list contains an invalid address")
			}
		}
	}
	return nil
}

func validateLarkWebhook(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || (parsed.Hostname() != "open.feishu.cn" && parsed.Hostname() != "open.larksuite.com") || !strings.HasPrefix(parsed.Path, "/open-apis/bot/v2/hook/") {
		return errors.New("lark bot URL is invalid")
	}
	return nil
}

func sanitizeEndpoint(raw string) string {
	return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(raw), "/"), "/v1")
}

func validateEndpoint(raw string) error {
	if raw == "" {
		return errors.New("endpoint URL cannot be empty")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return errors.New("endpoint must start with http:// or https://")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("endpoint is not a valid URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || !gatewayEndpointPattern.MatchString(raw) {
		return errors.New("endpoint must not contain credentials, query parameters, fragments, or unsafe characters")
	}
	return nil
}
