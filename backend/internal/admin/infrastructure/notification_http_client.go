package infrastructure

import (
	"bosapi/backend/internal/admin/domain"
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

func newNotificationHTTPClient() *http.Client {
	transport := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, errors.New("webhook DNS lookup failed")
		}
		if len(ips) == 0 {
			return nil, errors.New("webhook DNS lookup failed")
		}
		for _, ip := range ips {
			if !domain.PublicWebhookIP(ip.IP) {
				return nil, errors.New("webhook address is not public")
			}
		}
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("webhook connection failed")
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
