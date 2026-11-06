package upstreamurl

import "strings"

// ModelID separates Gemini resource names from the ID used by routing and
// policy. Other protocols may have genuine slash-containing model identifiers.
func ModelID(apiFormat, name string) string {
	if apiFormat == "gemini" {
		id := strings.TrimPrefix(name, "models/")
		// Nested/invalid resource paths must not become valid after a second pass.
		if strings.Contains(id, "/") {
			return name
		}
		return id
	}
	return name
}
