// Package upstreamurl builds API endpoint URLs from configured upstream base URLs.
package upstreamurl

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var versionSegment = regexp.MustCompile(`(?i)^v[0-9]+(?:\.[0-9]+)*(?:(?:alpha|beta|preview)[0-9]*)?$`)

// BuildEndpoint joins an upstream base URL and API resource using the configured
// API format's default version. Existing version segments and complete resource
// paths in the base URL are preserved.
func BuildEndpoint(baseURL, apiFormat, resource string) (string, error) {
	base, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.User != nil || base.Fragment != "" {
		return "", errors.New("invalid upstream base URL")
	}
	resourceURL, err := url.Parse(resource)
	if err != nil || resourceURL.IsAbs() || resourceURL.Host != "" || resourceURL.RawQuery != "" || resourceURL.Fragment != "" {
		return "", errors.New("invalid upstream resource path")
	}
	resourcePath := strings.Trim(resourceURL.Path, "/")
	if resourcePath == "" {
		return "", errors.New("empty upstream resource path")
	}

	basePath := strings.TrimRight(base.Path, "/")
	if pathHasSuffix(basePath, resourcePath) {
		return base.String(), nil
	}
	if !hasVersionSegment(basePath) {
		if version := defaultVersion(apiFormat); version != "" {
			basePath += version
		}
	}
	base.Path = strings.TrimRight(basePath, "/") + "/" + resourcePath
	base.RawPath = ""
	return base.String(), nil
}

func defaultVersion(apiFormat string) string {
	switch apiFormat {
	case "custom":
		return ""
	case "gemini":
		return "/v1beta"
	default:
		return "/v1"
	}
}

func hasVersionSegment(path string) bool {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return false
	}
	lastSegment := path[strings.LastIndex(path, "/")+1:]
	return versionSegment.MatchString(lastSegment)
}

func pathHasSuffix(path, suffix string) bool {
	path = strings.TrimRight(path, "/")
	return path == "/"+suffix || strings.HasSuffix(path, "/"+suffix)
}
