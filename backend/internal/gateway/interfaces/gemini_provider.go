package interfaces

import "bosapi/backend/internal/platform/upstreamurl"

func normalizeGeminiModels(models []string) []string {
	normalized := make([]string, len(models))
	for index, model := range models {
		normalized[index] = upstreamurl.ModelID("gemini", model)
	}
	return normalized
}

// Normalize copies rather than changing stored provider configuration. Old
// resource names, allowlists, aliases and prices remain usable after deployment.
func normalizeGeminiProvider(p provider) provider {
	if p.APIFormat != "gemini" {
		return p
	}
	p.Models = normalizeGeminiModels(p.Models)
	mapping := map[string]string{}
	for name, target := range p.ModelMapping {
		id := upstreamurl.ModelID("gemini", name)
		if name != id {
			if _, canonical := p.ModelMapping[id]; canonical {
				continue
			}
		}
		mapping[id] = upstreamurl.ModelID("gemini", target)
	}
	p.ModelMapping = mapping
	pricing := map[string]any{}
	for name, value := range p.Pricing {
		pricing[name] = value
	}
	if prices, ok := p.Pricing["models"].(map[string]any); ok {
		normalized := map[string]any{}
		for name, value := range prices {
			id := upstreamurl.ModelID("gemini", name)
			if name != id {
				if _, canonical := prices[id]; canonical {
					continue
				}
			}
			normalized[id] = value
		}
		pricing["models"] = normalized
	}
	p.Pricing = pricing
	return p
}
