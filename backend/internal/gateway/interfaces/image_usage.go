package interfaces

import (
	"encoding/json"
	"net/http"
)

// imageUsageObserver removes large JSON strings before observing usage. Image
// payloads can be many megabytes, including in a single SSE completed event.
// Retained metadata is bounded independently of the image size.
type imageUsageObserver struct {
	state                        *usageMetricsState
	streaming                    bool
	inString, escaped, oversized bool
	token, body                  []byte
	overflow                     bool
}

func (o *imageUsageObserver) write(chunk []byte) {
	var compact []byte
	for _, b := range chunk {
		if !o.inString {
			if b == '"' {
				o.inString = true
				o.token = append(o.token[:0], b)
			} else {
				compact = append(compact, b)
			}
			continue
		}
		if !o.oversized {
			if len(o.token) < 256 {
				o.token = append(o.token, b)
			} else {
				o.oversized = true
				o.token = o.token[:0]
			}
		}
		if o.escaped {
			o.escaped = false
			continue
		}
		if b == '\\' {
			o.escaped = true
			continue
		}
		if b == '"' {
			if o.oversized {
				compact = append(compact, '"', '"')
			} else {
				compact = append(compact, o.token...)
			}
			o.inString, o.oversized = false, false
			o.token = o.token[:0]
		}
	}
	if o.streaming {
		observeSSEUsage(o.state, compact, false)
	} else if !o.overflow {
		if len(o.body)+len(compact) > 1<<20 {
			o.overflow = true
			o.body = nil
		} else {
			o.body = append(o.body, compact...)
		}
	}
}

func (o *imageUsageObserver) finish() {
	if o.streaming {
		observeSSEUsage(o.state, nil, true)
		return
	}
	if o.overflow || o.inString {
		return
	}
	var value map[string]any
	if json.Unmarshal(o.body, &value) == nil {
		observeUsageValue(o.state, value, "response.completed")
	}
}

func newImageUsageObserver(r *http.Request, streaming bool) *imageUsageObserver {
	state := usageState(r)
	if state != nil && streaming {
		state.streaming = true
	}
	return &imageUsageObserver{state: state, streaming: streaming}
}
