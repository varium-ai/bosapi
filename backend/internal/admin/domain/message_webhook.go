package domain

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ValidateMessageWebhookURL permits public HTTPS receivers, including secret paths and query parameters.
// DNS answers are checked again by the notification transport when connecting.
func ValidateMessageWebhookURL(value string) error {
	invalid := errors.New("webhook URL must be a public HTTPS URL without credentials or fragments")
	parsed, err := url.Parse(value)
	if err != nil || len(value) > 2048 || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return invalid
	}
	host := strings.TrimRight(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.Contains(host, "%") {
		return invalid
	}
	if ip := net.ParseIP(host); ip != nil && !PublicWebhookIP(ip) {
		return invalid
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return invalid
		}
	}
	return nil
}

// PublicWebhookIP protects the authentication notification transport from SSRF.
func PublicWebhookIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		_, subnet, _ := net.ParseCIDR(block)
		if subnet.Contains(ip) {
			return false
		}
	}
	return true
}
