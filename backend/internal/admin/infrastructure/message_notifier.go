package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/mail"
	"strings"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/logging"
)

// ConfiguredMessageNotifier resolves delivery settings from the database on each send.
type ConfiguredMessageNotifier struct {
	settings      domain.SystemSettingRepository
	webhookClient *http.Client
}

// NewConfiguredMessageNotifier uses the shared notification settings for all deliveries.
func NewConfiguredMessageNotifier(settings domain.SystemSettingRepository) *ConfiguredMessageNotifier {
	return &ConfiguredMessageNotifier{settings: settings, webhookClient: newNotificationHTTPClient()}
}

func (notifier *ConfiguredMessageNotifier) SendOTP(ctx context.Context, email, code string) error {
	config, err := notifier.read(ctx)
	if err != nil {
		return err
	}
	switch config.Provider {
	case "lark":
		return NewFeishuOTPNotifier(config.LarkWebhook).SendOTP(ctx, email, code)
	case "webhook":
		return sendMessageWebhook(ctx, notifier.webhookClient, config.WebhookURL, messageWebhookPayload{
			Type: "otp", Email: email, Code: code, ExpiresIn: 300,
			Subject: "BOS API verification code", Text: fmt.Sprintf("Your BOS API verification code is %s. It expires in 5 minutes.", code),
		})
	case "email":
		if _, err := mail.ParseAddress(email); err != nil {
			return errors.New("OTP recipient email is invalid")
		}
		return notifier.sendEmail(ctx, config, []string{email}, "BOS API verification code", fmt.Sprintf("Your BOS API verification code is %s. It expires in 5 minutes.", code))
	default:
		return errors.New("message notification provider is not configured")
	}
}

// SendAlert sends operational notifications to the configured distribution list.
func (notifier *ConfiguredMessageNotifier) SendAlert(ctx context.Context, subject, body string) error {
	config, err := notifier.read(ctx)
	if err != nil {
		return err
	}
	if config.Provider != "email" {
		return errors.New("alert email delivery requires email provider")
	}
	if len(config.AlertEmails) == 0 {
		return errors.New("alert email list is not configured")
	}
	return notifier.sendEmail(ctx, config, config.AlertEmails, subject, body)
}

func (notifier *ConfiguredMessageNotifier) read(ctx context.Context) (domain.MessageNotificationConfig, error) {
	setting, err := notifier.settings.Get(ctx, application.SettingKeyMessageNotifications)
	if err != nil {
		return domain.MessageNotificationConfig{}, errors.New("message notification settings are unavailable")
	}
	if setting == nil || strings.TrimSpace(setting.ValueText) == "" {
		return domain.MessageNotificationConfig{}, errors.New("message notification settings are not configured")
	}
	var config domain.MessageNotificationConfig
	if err := json.Unmarshal([]byte(setting.ValueText), &config); err != nil {
		return domain.MessageNotificationConfig{}, errors.New("message notification settings are invalid")
	}
	return config, nil
}

func (notifier *ConfiguredMessageNotifier) sendEmail(ctx context.Context, config domain.MessageNotificationConfig, recipients []string, subject, body string) error {
	stage := "validate"
	fail := func(err error) error {
		logging.Event(ctx, "ERROR", "notification.email.send_failed", fmt.Sprintf("stage=%s smtp_host=%s smtp_port=%d recipients=%d err=%s", stage, config.SMTPHost, config.SMTPPort, len(recipients), err.Error()))
		if stage == "validate" {
			return err
		}
		return errors.New("email notification delivery failed")
	}
	logging.Event(ctx, "INFO", "notification.email.send_started", fmt.Sprintf("smtp_host=%s smtp_port=%d recipients=%d", config.SMTPHost, config.SMTPPort, len(recipients)))
	from, err := mail.ParseAddress(config.FromEmail)
	if err != nil || config.SMTPHost == "" || config.SMTPPort < 1 || config.SMTPPort > 65535 {
		return fail(errors.New("email notification settings are incomplete"))
	}
	to := make([]string, 0, len(recipients))
	headerTo := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		parsed, parseErr := mail.ParseAddress(strings.TrimSpace(recipient))
		if parseErr != nil {
			return fail(errors.New("email notification recipient is invalid"))
		}
		to = append(to, parsed.Address)
		headerTo = append(headerTo, parsed.String())
	}
	if len(to) == 0 {
		return fail(errors.New("email notification recipients are empty"))
	}
	fromName := strings.NewReplacer("\r", "", "\n", "").Replace(config.FromName)
	if fromName != "" {
		from.Name = fromName
	}
	message := "From: " + from.String() + "\r\nTo: " + strings.Join(headerTo, ", ") + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	client, cleanup, connectionStage, err := connectSMTP(ctx, config)
	if err != nil {
		stage = connectionStage
		return fail(err)
	}
	defer cleanup()
	stage = "mail_from"
	if err := client.Mail(from.Address); err != nil {
		return fail(err)
	}
	stage = "rcpt_to"
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fail(err)
		}
	}
	stage = "data"
	writer, err := client.Data()
	if err != nil {
		return fail(err)
	}
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return fail(err)
	}
	if err := writer.Close(); err != nil {
		return fail(err)
	}
	stage = "quit"
	if err := client.Quit(); err != nil {
		return fail(err)
	}
	logging.Event(ctx, "INFO", "notification.email.send_succeeded", fmt.Sprintf("smtp_host=%s smtp_port=%d recipients=%d", config.SMTPHost, config.SMTPPort, len(to)))
	return nil
}

var _ application.OTPNotifier = (*ConfiguredMessageNotifier)(nil)
