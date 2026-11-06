package domain

import (
	"context"
	"time"
)

// SystemSetting represents a generic persisted system configuration in the settings table.
type SystemSetting struct {
	SettingKey string    `json:"setting_key"`
	ValueText  string    `json:"value_text"`
	IsSecret   bool      `json:"is_secret"`
	UpdatedBy  *uint64   `json:"updated_by,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// GatewayEndpoint contains the API address configured for this installation.
type GatewayEndpoint struct {
	Endpoint string `json:"endpoint"`
}

// MessageNotificationConfig stores delivery configuration in the existing settings table.
type MessageNotificationConfig struct {
	Provider     string   `json:"provider"`
	LarkWebhook  string   `json:"lark_webhook,omitempty"`
	WebhookURL   string   `json:"webhook_url,omitempty"`
	SMTPHost     string   `json:"smtp_host,omitempty"`
	SMTPPort     int      `json:"smtp_port,omitempty"`
	SMTPUsername string   `json:"smtp_username,omitempty"`
	SMTPPassword string   `json:"smtp_password,omitempty"`
	FromEmail    string   `json:"from_email,omitempty"`
	FromName     string   `json:"from_name,omitempty"`
	AlertEmails  []string `json:"alert_emails,omitempty"`
}

// SystemSettingRepository persists system configurations in the settings table.
type SystemSettingRepository interface {
	Get(ctx context.Context, key string) (*SystemSetting, error)
	Set(ctx context.Context, key string, valueText string, isSecret bool, updatedBy *uint64) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]SystemSetting, error)
}
