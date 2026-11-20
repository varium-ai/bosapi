package infrastructure

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/textproto"
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

func TestSMTPOTPDeliveryUsesDataAcknowledgement(t *testing.T) {
	for _, test := range []struct {
		name      string
		dataReply string
		closeConn bool
		wantError bool
	}{
		{"accepted and quit", "250 queued", false, false},
		{"accepted and disconnected", "250 queued", true, false},
		{"rejected", "550 message rejected", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			served := make(chan error, 1)
			go func() {
				served <- serveSMTPOTP(t.Context(), listener, test.dataReply, test.closeConn)
			}()
			port := listener.Addr().(*net.TCPAddr).Port
			config := fmt.Sprintf(`{"provider":"email","smtp_host":"127.0.0.1","smtp_port":%d,"from_email":"sender@example.test"}`, port)
			notifier := NewConfiguredMessageNotifier(reportNotificationSettings{value: config})
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err = notifier.SendOTP(ctx, "recipient@example.test", "123456")
			select {
			case serverErr := <-served:
				if serverErr != nil {
					t.Fatal(serverErr)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if (err != nil) != test.wantError {
				t.Fatalf("SendOTP error = %v, want error = %t", err, test.wantError)
			}
		})
	}
}

func serveSMTPOTP(ctx context.Context, listener net.Listener, dataReply string, closeConn bool) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	wire := textproto.NewConn(conn)
	if err := wire.PrintfLine("220 fixture SMTP"); err != nil {
		return err
	}
	for {
		line, err := wire.ReadLine()
		if err != nil {
			return err
		}
		switch cmd, _, _ := strings.Cut(line, " "); cmd {
		case "EHLO", "MAIL", "RCPT":
			if err := wire.PrintfLine("250 OK"); err != nil {
				return err
			}
		case "DATA":
			if err := wire.PrintfLine("354 Continue"); err != nil {
				return err
			}
			body, err := wire.ReadDotBytes()
			if err != nil {
				return err
			}
			if !strings.Contains(string(body), "123456") {
				return fmt.Errorf("test email did not contain the verification code")
			}
			if err := wire.PrintfLine("%s", dataReply); err != nil || closeConn {
				return err
			}
		case "QUIT":
			return wire.PrintfLine("221 bye")
		default:
			return fmt.Errorf("unexpected SMTP command %s", cmd)
		}
	}
}
