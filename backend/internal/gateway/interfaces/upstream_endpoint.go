package interfaces

import (
	"errors"
	"net/url"

	"bosapi/backend/internal/platform/upstreamurl"
)

func providerEndpoint(p provider, resource string) (string, error) {
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
