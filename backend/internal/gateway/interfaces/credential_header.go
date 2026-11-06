package interfaces

import (
	"bytes"
	"net/http"
	"sort"
	"strings"
)

// gatewayCredentialHeaders names credentials used to authenticate the request
// to BOS API. They are transport credentials, not prompt content.
var gatewayCredentialHeaders = map[string]bool{
	"authorization":  true,
	"x-api-key":      true,
	"x-goog-api-key": true,
}

// isGatewayCredentialHeader reports whether the supplied header carries
// credentials intended for the gateway or provider. HTTP header names are
// case-insensitive, so this check must be too.
func isGatewayCredentialHeader(name string) bool {
	return gatewayCredentialHeaders[strings.ToLower(name)]
}

// requestHeaderSecurityPayload serializes non-credential request header values
// for the request-header scan path. It does not change upstream forwarding and
// does not scan the body; body keys remain deliberate leak signals.
func requestHeaderSecurityPayload(r *http.Request) []byte {
	names := make([]string, 0, len(r.Header))
	for name := range r.Header {
		if isGatewayCredentialHeader(name) {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	var payload bytes.Buffer
	for _, name := range names {
		for _, value := range r.Header[name] {
			payload.WriteString(name)
			payload.WriteString(": ")
			payload.WriteString(value)
			payload.WriteByte('\n')
		}
	}
	return payload.Bytes()
}
