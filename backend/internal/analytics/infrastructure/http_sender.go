package infrastructure

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bosapi/backend/internal/analytics/application"
	"bosapi/backend/internal/analytics/domain"
)

// HTTPSender has no business credentials, tracing middleware or body logging.
type HTTPSender struct{ client *http.Client }

func NewHTTPSender(timeout time.Duration) *HTTPSender {
	if timeout < time.Second || timeout > 10*time.Second {
		timeout = 5 * time.Second
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: timeout}).DialContext, TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout, MaxResponseHeaderBytes: 16 << 10, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second, ForceAttemptHTTP2: false}
	return &HTTPSender{client: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (s *HTTPSender) Send(ctx context.Context, endpoint string, body []byte) (application.SendResult, error) {
	if _, err := domain.DecodeReport(body); err != nil {
		return application.SendResult{}, domain.ErrInvalidPayload
	}
	receiver, err := url.Parse(endpoint)
	if err != nil || receiver.Scheme != "https" || receiver.Hostname() == "" || receiver.User != nil || receiver.RawQuery != "" || receiver.ForceQuery || receiver.Fragment != "" || strings.Contains(endpoint, "#") {
		return application.SendResult{}, domain.ErrConflict
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return application.SendResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return application.SendResult{}, err
	}
	defer response.Body.Close()
	// Never parse, retain or log response content, even on errors.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	return application.SendResult{Status: response.StatusCode, RetryAfter: retryAfter(response.Header.Get("Retry-After"), time.Now())}, nil
}

func retryAfter(raw string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseUint(raw, 10, 32); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(raw); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

func (s *HTTPSender) CloseIdleConnections() { s.client.CloseIdleConnections() }
