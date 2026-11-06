package infrastructure

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"strconv"
	"time"

	"bosapi/backend/internal/admin/domain"
)

// TestSMTP checks the saved SMTP handshake and authentication without sending mail.
func (notifier *ConfiguredMessageNotifier) TestSMTP(ctx context.Context) (bool, error) {
	config, err := notifier.read(ctx)
	if err != nil {
		return false, err
	}
	if config.Provider != "email" || config.SMTPHost == "" || config.SMTPPort < 1 || config.SMTPPort > 65535 {
		return false, errors.New("SMTP is not configured")
	}
	client, cleanup, _, err := connectSMTP(ctx, config)
	if err != nil {
		return false, errors.New("SMTP connection test failed")
	}
	defer cleanup()
	_, encrypted := client.TLSConnectionState()
	if err := client.Quit(); err != nil {
		return false, errors.New("SMTP connection test failed")
	}
	return encrypted, nil
}

// connectSMTP is shared by delivery and diagnostics so a probe tests the real transport.
func connectSMTP(ctx context.Context, config domain.MessageNotificationConfig) (client *smtp.Client, cleanup func(), stage string, err error) {
	cleanup = func() {}
	address := net.JoinHostPort(config.SMTPHost, strconv.Itoa(config.SMTPPort))
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	var connection net.Conn
	stage = "dial"
	if config.SMTPPort == 465 {
		tlsDialer := tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: config.SMTPHost, MinVersion: tls.VersionTLS12}}
		connection, err = tlsDialer.DialContext(ctx, "tcp", address)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	cleanup = func() { stop(); _ = connection.Close() }
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	stage = "set_deadline"
	if err = connection.SetDeadline(deadline); err != nil {
		return
	}
	stage = "smtp_handshake"
	if client, err = smtp.NewClient(connection, config.SMTPHost); err != nil {
		return
	}
	if config.SMTPPort != 465 {
		stage = "starttls"
		if ok, _ := client.Extension("STARTTLS"); ok {
			err = client.StartTLS(&tls.Config{ServerName: config.SMTPHost, MinVersion: tls.VersionTLS12})
			if err != nil {
				return
			}
		} else if config.SMTPUsername != "" {
			err = errors.New("SMTP server does not support STARTTLS")
			return
		}
	}
	if config.SMTPUsername != "" {
		stage = "auth"
		err = client.Auth(smtp.PlainAuth("", config.SMTPUsername, config.SMTPPassword, config.SMTPHost))
	}
	return
}
