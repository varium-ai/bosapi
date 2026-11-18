package interfaces

import (
	"errors"
	"net/url"
	"strings"

	"bosapi/backend/internal/platform/upstreamurl"
)

func providerEndpoint(p provider, resource string) (string, error) {
	if p.APIFormat == "openai_chat" && strings.Trim(resource, "/") == "chat/completions" {
		if base, err := url.Parse(p.BaseURL); err == nil && strings.HasSuffix(strings.TrimRight(base.Path, "/"), "/responses") {
			base.Path = strings.TrimSuffix(strings.TrimRight(base.Path, "/"), "/responses")
			base.RawPath = ""
			p.BaseURL = base.String()
		}
	}
	return upstreamurl.BuildEndpoint(p.BaseURL, p.APIFormat, resource)
}

func providerWebSocketEndpoint(p provider, resource string) (string, error) {
	endpoint, err := providerEndpoint(p, resource)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", errors.New("unsupported upstream scheme")
	}
	return parsed.String(), nil
}

func providerWebSocketEndpointWithQuery(p provider, resource string, query url.Values) (string, error) {
	endpoint, err := providerWebSocketEndpoint(p, resource)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
