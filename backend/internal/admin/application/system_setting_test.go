package application

import (
	"context"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

type memorySystemSettingRepo struct {
	settings map[string]domain.SystemSetting
}

func TestGenericSettingsCannotExposeOrRemoveCommunityCapacityLock(t *testing.T) {
	ctx := context.Background()
	repo := newMemorySystemSettingRepo()
	key := "community.capacity_lock"
	if err := repo.Set(ctx, key, "", false, nil); err != nil {
		t.Fatal(err)
	}
	service := NewSystemSettingService(repo)
	if err := service.SetSetting(ctx, key, "changed", false, nil); err == nil {
		t.Fatal("internal lock was writable")
	}
	if err := service.DeleteSetting(ctx, key); err == nil {
		t.Fatal("internal lock was deletable")
	}
	if _, exists := repo.settings[key]; !exists {
		t.Fatal("capacity lock was removed")
	}
	if setting, err := service.GetSetting(ctx, key); err != nil || setting != nil {
		t.Fatal("internal lock was exposed")
	}
	if settings, err := service.ListSettings(ctx, ""); err != nil || len(settings) != 0 {
		t.Fatal("internal lock appeared in settings list")
	}
}

func newMemorySystemSettingRepo() *memorySystemSettingRepo {
	return &memorySystemSettingRepo{
		settings: make(map[string]domain.SystemSetting),
	}
}

func (m *memorySystemSettingRepo) Get(ctx context.Context, key string) (*domain.SystemSetting, error) {
	if s, ok := m.settings[key]; ok {
		return &s, nil
	}
	return nil, nil
}

func (m *memorySystemSettingRepo) Set(ctx context.Context, key string, valueText string, isSecret bool, updatedBy *uint64) error {
	m.settings[key] = domain.SystemSetting{
		SettingKey: key,
		ValueText:  valueText,
		IsSecret:   isSecret,
		UpdatedBy:  updatedBy,
		UpdatedAt:  time.Now(),
	}
	return nil
}

func (m *memorySystemSettingRepo) Delete(ctx context.Context, key string) error {
	delete(m.settings, key)
	return nil
}

func (m *memorySystemSettingRepo) List(ctx context.Context, prefix string) ([]domain.SystemSetting, error) {
	var list []domain.SystemSetting
	for _, s := range m.settings {
		list = append(list, s)
	}
	return list, nil
}

func TestSystemSettingService_GatewayEndpoint(t *testing.T) {
	// Neither old environment overrides nor the console origin supply defaults.
	t.Setenv("BOS_GATEWAY_ENDPOINT", "https://deployment.example.com")
	t.Setenv("BOS_GATEWAY_PROD_ENDPOINT", "https://production.example.com")
	t.Setenv("BOS_GATEWAY_TEST_ENDPOINT", "https://staging.example.com")
	t.Setenv("BOS_PRIMARY_DOMAIN", "https://console.example.com")
	repo := newMemorySystemSettingRepo()
	service := NewSystemSettingService(repo)
	ctx := context.Background()

	initial, err := service.GetGatewayEndpoint(ctx)
	if err != nil || initial.Endpoint != "" || len(repo.settings) != 0 {
		t.Fatalf("new installation must remain unconfigured: %+v err=%v", initial, err)
	}
	assertPublicGatewayEndpoint(t, service, "")

	userID := uint64(42)
	updated, err := service.UpdateGatewayEndpoint(ctx, domain.GatewayEndpoint{
		Endpoint: " http://api.mycompany.com/proxy/v1/ ",
	}, &userID)
	if err != nil || updated.Endpoint != "http://api.mycompany.com/proxy" {
		t.Fatalf("unexpected normalized endpoint: %+v err=%v", updated, err)
	}
	persisted, err := service.GetGatewayEndpoint(ctx)
	if err != nil || persisted != updated {
		t.Fatalf("saved address did not round-trip: %+v err=%v", persisted, err)
	}
	setting := repo.settings[SettingKeyGatewayEndpoints]
	if setting.ValueText != `{"endpoint":"http://api.mycompany.com/proxy"}` || setting.IsSecret || setting.UpdatedBy == nil || *setting.UpdatedBy != userID {
		t.Fatalf("expected one saved address and updater attribution: %+v", setting)
	}
	assertPublicGatewayEndpoint(t, service, updated.Endpoint)

	cleared, err := service.ClearGatewayEndpoint(ctx)
	if err != nil || cleared.Endpoint != "" || len(repo.settings) != 0 {
		t.Fatalf("clearing must leave no default address: %+v err=%v", cleared, err)
	}
	assertPublicGatewayEndpoint(t, service, "")
}

func assertPublicGatewayEndpoint(t *testing.T, service *SystemSettingService, expected string) {
	t.Helper()
	config, err := service.GetPublicPlatformConfig(context.Background())
	if err != nil || len(config) != 1 || config["gateway_endpoint"] != expected {
		t.Fatalf("unexpected public configuration: %+v err=%v; want %q", config, err, expected)
	}
}

func TestGatewayEndpointReadsSavedSettingsWithoutInventingAnAddress(t *testing.T) {
	for _, tc := range []struct {
		name, stored, expected string
	}{
		{"single", `{"endpoint":"https://gateway.example.com/proxy/v1/"}`, "https://gateway.example.com/proxy"},
		{"legacy production", `{"prod_endpoint":"https://production.example.com/v1/","test_endpoint":"https://staging.example.com","active_environment":"prod"}`, "https://production.example.com"},
		{"legacy test", `{"prod_endpoint":"https://production.example.com","test_endpoint":"https://staging.example.com/v1/","active_environment":"test"}`, "https://staging.example.com"},
		{"legacy missing selection", `{"prod_endpoint":"https://production.example.com","test_endpoint":"https://staging.example.com"}`, "https://staging.example.com"},
		{"empty", `{"endpoint":""}`, ""},
		{"single takes precedence", `{"endpoint":"https://gateway.example.com","test_endpoint":"https://staging.example.com"}`, "https://gateway.example.com"},
		{"empty takes precedence", `{"endpoint":"","test_endpoint":"https://staging.example.com"}`, ""},
		{"legacy selection missing address", `{"prod_endpoint":"https://production.example.com","active_environment":"test"}`, ""},
		{"legacy invalid selection", `{"test_endpoint":"https://staging.example.com","active_environment":"other"}`, ""},
		{"unsafe single", `{"endpoint":"https://user:secret@gateway.example.com"}`, ""},
		{"unsafe legacy", `{"test_endpoint":"https://gateway.example.com?key=secret"}`, ""},
		{"corrupt", `{broken`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemorySystemSettingRepo()
			ctx := context.Background()
			if err := repo.Set(ctx, SettingKeyGatewayEndpoints, tc.stored, false, nil); err != nil {
				t.Fatal(err)
			}
			service := NewSystemSettingService(repo)
			assertPublicGatewayEndpoint(t, service, tc.expected)
			if repo.settings[SettingKeyGatewayEndpoints].ValueText != tc.stored {
				t.Fatal("reading must not rewrite stored settings")
			}
			if _, err := service.UpdateGatewayEndpoint(ctx, domain.GatewayEndpoint{Endpoint: "https://new.example.com"}, nil); err != nil {
				t.Fatal(err)
			}
			if repo.settings[SettingKeyGatewayEndpoints].ValueText != `{"endpoint":"https://new.example.com"}` {
				t.Fatal("saving must retain only the single address")
			}
		})
	}
}

func TestGatewayEndpointValidationDoesNotOverwriteSettings(t *testing.T) {
	ctx := context.Background()
	service := NewSystemSettingService(newMemorySystemSettingRepo())
	valid := domain.GatewayEndpoint{Endpoint: "https://gateway.example.com"}
	if _, err := service.UpdateGatewayEndpoint(ctx, valid, nil); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", " ", "not-a-url", "ftp://gateway.example.com", "https://user:secret@example.com", "https://gateway.example.com?key=secret", "https://gateway.example.com/#fragment", "https://gateway.example.com/$(whoami)", "https://gateway.example.com/\"quoted\""} {
		if _, err := service.UpdateGatewayEndpoint(ctx, domain.GatewayEndpoint{Endpoint: value}, nil); err == nil {
			t.Fatalf("expected invalid URL to be rejected: %q", value)
		}
	}
	assertPublicGatewayEndpoint(t, service, valid.Endpoint)
}

func TestMessageNotificationSettingsPersistSecretsAndValidate(t *testing.T) {
	repo := newMemorySystemSettingRepo()
	service := NewSystemSettingService(repo)
	ctx := context.Background()
	config := domain.MessageNotificationConfig{
		Provider: "lark", LarkWebhook: "https://open.feishu.cn/open-apis/bot/v2/hook/secret",
		AlertEmails: []string{"ops@example.com"},
	}
	if err := service.UpdateMessageNotificationConfig(ctx, config, nil); err != nil {
		t.Fatal(err)
	}
	larkPersisted, err := service.GetMessageNotificationConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(larkPersisted.AlertEmails) != 0 {
		t.Fatalf("expected alert emails to be empty for lark, got: %+v", larkPersisted.AlertEmails)
	}
	if !repo.settings[SettingKeyMessageNotifications].IsSecret {
		t.Fatal("expected message notification settings to be stored as secret")
	}
	config.Provider = "email"
	config.LarkWebhook = ""
	config.SMTPHost = "smtp.example.com"
	config.SMTPPort = 587
	config.FromEmail = "notify@example.com"
	config.SMTPPassword = "smtp-secret"
	if err := service.UpdateMessageNotificationConfig(ctx, config, nil); err != nil {
		t.Fatal(err)
	}
	persisted, err := service.GetMessageNotificationConfig(ctx)
	if err != nil || persisted.LarkWebhook == "" || persisted.SMTPPassword != "smtp-secret" {
		t.Fatalf("secret values were not retained: %+v err=%v", persisted, err)
	}
	if err := service.UpdateMessageNotificationConfig(ctx, domain.MessageNotificationConfig{Provider: "lark", LarkWebhook: "http://127.0.0.1/hook"}, nil); err == nil {
		t.Fatal("expected invalid lark URL to be rejected")
	}
}

func TestMessageWebhookSettingsPreserveAndReplaceSecretURL(t *testing.T) {
	repo := newMemorySystemSettingRepo()
	service := NewSystemSettingService(repo)
	ctx := context.Background()
	const original = "https://receiver.example.com/hook/PRIVATE_PATH?token=PRIVATE_TOKEN"
	config := domain.MessageNotificationConfig{
		Provider: "webhook", WebhookURL: "  " + original + "  ",
		LarkWebhook: "https://open.feishu.cn/open-apis/bot/v2/hook/secret", SMTPPassword: "smtp-secret",
		AlertEmails: []string{"ops@example.com"},
	}
	if err := service.UpdateMessageNotificationConfig(ctx, config, nil); err != nil {
		t.Fatal(err)
	}
	persisted, err := service.GetMessageNotificationConfig(ctx)
	if err != nil || persisted.Provider != "webhook" || persisted.WebhookURL != original || len(persisted.AlertEmails) != 0 || !repo.settings[SettingKeyMessageNotifications].IsSecret {
		t.Fatalf("webhook configuration was not stored correctly: %v", err)
	}
	for _, next := range []domain.MessageNotificationConfig{
		{Provider: "email", SMTPHost: "smtp.example.com", SMTPPort: 587, FromEmail: "notify@example.com"},
		{Provider: "lark"},
		{Provider: "webhook"},
	} {
		if err := service.UpdateMessageNotificationConfig(ctx, next, nil); err != nil {
			t.Fatal(err)
		}
		persisted, err = service.GetMessageNotificationConfig(ctx)
		if err != nil || persisted.WebhookURL != original || persisted.LarkWebhook != config.LarkWebhook || persisted.SMTPPassword != "smtp-secret" {
			t.Fatalf("switching providers lost saved credentials: %v", err)
		}
	}
	const replacement = "https://other.example.com/new?key=NEW_PRIVATE_TOKEN"
	if err := service.UpdateMessageNotificationConfig(ctx, domain.MessageNotificationConfig{Provider: "webhook", WebhookURL: replacement}, nil); err != nil {
		t.Fatal(err)
	}
	persisted, err = service.GetMessageNotificationConfig(ctx)
	if err != nil || persisted.WebhookURL != replacement {
		t.Fatalf("replacement was not persisted: %v", err)
	}
	before := repo.settings[SettingKeyMessageNotifications].ValueText
	if err := service.UpdateMessageNotificationConfig(ctx, domain.MessageNotificationConfig{Provider: "webhook", WebhookURL: "http://127.0.0.1/PRIVATE"}, nil); err == nil {
		t.Fatal("invalid receiver accepted")
	}
	if repo.settings[SettingKeyMessageNotifications].ValueText != before {
		t.Fatal("rejected change overwrote existing configuration")
	}
	if err := NewSystemSettingService(newMemorySystemSettingRepo()).UpdateMessageNotificationConfig(ctx, domain.MessageNotificationConfig{Provider: "webhook"}, nil); err == nil {
		t.Fatal("new webhook configuration accepted without a URL")
	}
}
