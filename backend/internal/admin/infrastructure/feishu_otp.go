package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// FeishuOTPNotifier sends OTP codes to a configured Feishu bot webhook.
type FeishuOTPNotifier struct{ webhook string }

// NewFeishuOTPNotifier constructs a webhook notifier without logging its secret URL.
func NewFeishuOTPNotifier(webhook string) *FeishuOTPNotifier {
	return &FeishuOTPNotifier{webhook: webhook}
}

// SendOTP delivers a code and returns only sanitized delivery failures.
func (notifier *FeishuOTPNotifier) SendOTP(ctx context.Context, email, code string) error {
	text := fmt.Sprintf("Administrator verification code for %s: %s. It expires in 5 minutes.", email, code)
	if email == "" {
		text = code
	}
	return notifier.SendText(ctx, text)
}

// SendText reuses the restricted Feishu transport without persisting webhook secrets or responses.
func (notifier *FeishuOTPNotifier) SendText(ctx context.Context, text string) error {
	parsed, err := url.Parse(notifier.webhook)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || !validFeishuHost(parsed.Hostname()) || !strings.HasPrefix(parsed.Path, "/open-apis/bot/v2/hook/") {
		return errors.New("feishu otp delivery is unavailable")
	}
	transport := &http.Transport{DialContext: func(dialCtx context.Context, _, address string) (net.Conn, error) {
		return dialFeishu(dialCtx, address, parsed.Hostname())
	}}
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer transport.CloseIdleConnections()
	return sendFeishuText(ctx, client, notifier.webhook, text)
}

func sendFeishuText(ctx context.Context, client *http.Client, webhook, text string) error {
	payload, err := json.Marshal(map[string]any{"msg_type": "text", "content": map[string]string{"text": text}})
	if err != nil {
		return errors.New("feishu otp delivery is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(payload))
	if err != nil {
		return errors.New("feishu otp delivery is unavailable")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return errors.New("feishu otp delivery is unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("feishu otp delivery is unavailable")
	}
	var result struct {
		Code       *int `json:"code"`
		StatusCode *int `json:"StatusCode"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Code == nil && result.StatusCode == nil ||
		result.Code != nil && *result.Code != 0 || result.StatusCode != nil && *result.StatusCode != 0 {
		return errors.New("feishu otp delivery is unavailable")
	}
	return nil
}

func validFeishuHost(host string) bool {
	return host == "open.feishu.cn" || host == "open.larksuite.com"
}

func dialFeishu(ctx context.Context, address, host string) (net.Conn, error) {
	requestedHost, port, err := net.SplitHostPort(address)
	if err != nil || requestedHost != host {
		return nil, errors.New("feishu otp delivery is unavailable")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("feishu otp delivery is unavailable")
	}
	for _, candidate := range addresses {
		ip, ok := netip.AddrFromSlice(candidate.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			return nil, errors.New("feishu otp delivery is unavailable")
		}
		connection, dialErr := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
	}
	return nil, errors.New("feishu otp delivery is unavailable")
}
