package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"time"
)

type Certificate struct {
	Target    string     `json:"target"`
	Status    string     `json:"status"`
	CheckedAt time.Time  `json:"checked_at"`
	Subject   string     `json:"subject,omitempty"`
	Issuer    string     `json:"issuer,omitempty"`
	NotBefore *time.Time `json:"not_before,omitempty"`
	NotAfter  *time.Time `json:"not_after,omitempty"`
	Days      *int       `json:"days_remaining,omitempty"`
}

// Targets come only from operator configuration, never Host headers or user-supplied URLs.
func checkCertificates(ctx context.Context, configured string) []Certificate {
	certs := []Certificate{}
	seen := map[string]bool{}
	for _, target := range strings.Split(configured, ",") {
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		if len(certs) == 4 {
			break
		}
		seen[target] = true
		result := Certificate{Target: target, Status: "invalid_target", CheckedAt: time.Now()}
		host, port, err := net.SplitHostPort(target)
		if err != nil || host == "" || port != "443" || strings.ContainsAny(host, "/@?# ") {
			certs = append(certs, result)
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
		conn, err := dialer.DialContext(probeCtx, "tcp", target)
		cancel()
		if err != nil {
			result.Status = "unreachable"
			var verification *tls.CertificateVerificationError
			if errors.As(err, &verification) {
				result.Status = "invalid"
				if len(verification.UnverifiedCertificates) > 0 {
					certificateDetails(&result, verification.UnverifiedCertificates[0])
				}
			}
		} else {
			state := conn.(*tls.Conn).ConnectionState()
			conn.Close()
			result.Status = "ok"
			if len(state.PeerCertificates) > 0 {
				certificateDetails(&result, state.PeerCertificates[0])
			}
			if result.Status == "ok" && result.Days != nil && *result.Days <= 30 {
				result.Status = "expiring"
			}
		}
		certs = append(certs, result)
	}
	return certs
}

func certificateDetails(result *Certificate, cert *x509.Certificate) {
	result.Subject, result.Issuer = cert.Subject.CommonName, cert.Issuer.CommonName
	result.NotBefore, result.NotAfter = &cert.NotBefore, &cert.NotAfter
	days := int(time.Until(cert.NotAfter).Hours() / 24)
	result.Days = &days
	if time.Now().After(cert.NotAfter) {
		result.Status = "expired"
	}
	if time.Now().Before(cert.NotBefore) {
		result.Status = "not_yet_valid"
	}
}
