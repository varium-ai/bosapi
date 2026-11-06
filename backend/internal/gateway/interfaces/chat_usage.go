package interfaces

import "encoding/json"

// Chat streams omit usage unless it is explicitly requested. Always collect the
// upstream counts for accounting, even when the caller opts out of the usage
// chunk; otherwise successful requests consume no token quota.
func requestChatStreamUsage(payload []byte, path string) []byte {
	if path != "/chat/completions" {
		return payload
	}
	var request struct {
		Stream        bool            `json:"stream"`
		StreamOptions json.RawMessage `json:"stream_options"`
	}
	if json.Unmarshal(payload, &request) != nil || !request.Stream {
		return payload
	}
	var options map[string]json.RawMessage
	if len(request.StreamOptions) > 0 {
		if json.Unmarshal(request.StreamOptions, &options) != nil {
			// Preserve malformed options so the upstream can reject the request.
			return payload
		}
	}
	if options == nil {
		options = make(map[string]json.RawMessage)
	}
	options["include_usage"] = json.RawMessage(`true`)
	raw, err := json.Marshal(options)
	if err != nil {
		return payload
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return payload
	}
	fields["stream_options"] = raw
	// RawMessage keeps provider extensions and large integer values intact.
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return payload
	}
	return rewritten
}
