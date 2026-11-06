package infrastructure

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

func smtpProbeServer(t *testing.T, greeting bool) (string, int, <-chan []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	commands := make(chan []string, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			commands <- nil
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		var received []string
		defer func() { commands <- received }()
		if greeting {
			_, _ = fmt.Fprint(connection, "220 probe SMTP\r\n")
		}
		reader := bufio.NewReader(connection)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			received = append(received, line)
			reply := "250 OK\r\n"
			if strings.HasPrefix(line, "QUIT") {
				reply = "221 bye\r\n"
			}
			_, _ = fmt.Fprint(connection, reply)
			if strings.HasPrefix(line, "QUIT") {
				return
			}
		}
	}()
	return "127.0.0.1", listener.Addr().(*net.TCPAddr).Port, commands
}

func TestSMTPProbeDoesNotSendMail(t *testing.T) {
	host, port, commands := smtpProbeServer(t, true)
	config := fmt.Sprintf(`{"provider":"email","smtp_host":%q,"smtp_port":%d}`, host, port)
	notifier := NewConfiguredMessageNotifier(reportNotificationSettings{value: config})
	encrypted, err := notifier.TestSMTP(context.Background())
	if err != nil || encrypted {
		t.Fatalf("probe: encrypted=%v err=%v", encrypted, err)
	}
	received := <-commands
	if len(received) == 0 {
		t.Fatal("SMTP handshake was not checked")
	}
	for _, command := range received {
		if strings.HasPrefix(command, "MAIL") || strings.HasPrefix(command, "RCPT") || strings.HasPrefix(command, "DATA") {
			t.Fatalf("probe attempted email delivery: %q", received)
		}
	}
}

func TestSMTPProbeRequiresTLSBeforeAuthentication(t *testing.T) {
	host, port, commands := smtpProbeServer(t, true)
	config := fmt.Sprintf(`{"provider":"email","smtp_host":%q,"smtp_port":%d,"smtp_username":"admin","smtp_password":"private-password"}`, host, port)
	notifier := NewConfiguredMessageNotifier(reportNotificationSettings{value: config})
	if _, err := notifier.TestSMTP(context.Background()); err == nil || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("unsafe probe result: %v", err)
	}
	for _, command := range <-commands {
		if strings.HasPrefix(command, "AUTH") {
			t.Fatal("credentials sent over plaintext")
		}
	}
}

func TestSMTPConnectionCancellationInterruptsGreeting(t *testing.T) {
	host, port, commands := smtpProbeServer(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	started := time.Now()
	_, _, _, err := connectSMTP(ctx, domain.MessageNotificationConfig{SMTPHost: host, SMTPPort: port})
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("cancellation failed: duration=%v err=%v", time.Since(started), err)
	}
	<-commands
}

func TestSMTPProbeRejectsNonEmailConfiguration(t *testing.T) {
	notifier := NewConfiguredMessageNotifier(reportNotificationSettings{value: `{"provider":"lark"}`})
	if _, err := notifier.TestSMTP(context.Background()); err == nil {
		t.Fatal("unconfigured SMTP passed")
	}
}
