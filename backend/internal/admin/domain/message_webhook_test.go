package domain

import "testing"

func TestMessageWebhookURLValidation(t *testing.T) {
	for _, value := range []string{
		"https://receiver.example.com",
		"https://receiver.example.com/hooks/secret?token=secret&source=bos",
		"https://receiver.example.com:8443/notify",
		"https://8.8.8.8/hook",
		"https://[2606:4700:4700::1111]/hook",
	} {
		if err := ValidateMessageWebhookURL(value); err != nil {
			t.Errorf("valid receiver rejected: %s: %v", value, err)
		}
	}
	for _, value := range []string{
		"", "not a url", "http://receiver.example.com/hook", "ftp://receiver.example.com/hook",
		"https:///hook", "https://user:secret@receiver.example.com/hook", "https://receiver.example.com/hook#secret",
		"https://localhost/hook", "https://LOCALHOST./hook", "https://app.localhost/hook",
		"https://127.0.0.1/hook", "https://127.0.0.1./hook", "https://10.0.0.1/hook", "https://169.254.169.254/hook",
		"https://[::1]/hook", "https://[::ffff:127.0.0.1]/hook", "https://[fe80::1%25en0]/hook",
		"https://receiver.example.com:0/hook", "https://receiver.example.com:65536/hook",
		"https://receiver.example.com/hook\nInjected: value",
	} {
		if err := ValidateMessageWebhookURL(value); err == nil {
			t.Errorf("invalid receiver accepted: %q", value)
		}
	}
}
