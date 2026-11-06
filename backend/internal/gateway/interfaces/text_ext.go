package interfaces

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tiktoken-go/tokenizer"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
)

func tokenEstimate(value any) (int, error) {
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		return 0, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	return codec.Count(string(raw))
}

func (a *app) countTokens(w http.ResponseWriter, r *http.Request, p provider) {
	var request struct {
		Model    string            `json:"model"`
		Messages []json.RawMessage `json:"messages"`
		System   json.RawMessage   `json:"system"`
	}
	if body(r, &request) != nil || request.Model == "" || len(request.Messages) == 0 {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	if p.Type == "api_key" {
		payload, _ := json.Marshal(map[string]any{"model": request.Model, "input": request.Messages})
		res, err := a.direct(r.Context(), p, "POST", "/responses/input_tokens", payload)
		if err == nil {
			defer res.Body.Close()
			if res.StatusCode == 200 {
				var value struct {
					InputTokens int `json:"input_tokens"`
				}
				if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&value) == nil {
					respond(w, 200, map[string]int{"input_tokens": value.InputTokens})
					return
				}
			}
		}
	}
	count, err := tokenEstimate(request.Messages)
	if err != nil {
		fail(w, 500, errorcode.InternalError)
		return
	}
	if len(request.System) > 0 {
		extra, err := tokenEstimate(request.System)
		if err == nil {
			count += extra
		}
	}
	respond(w, 200, map[string]int{"input_tokens": count + 4*len(request.Messages) + 2})
}

func (a *app) inputTokens(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	if body(r, &request) != nil || request.Model == "" || len(request.Input) == 0 {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	count, err := tokenEstimate(request.Input)
	if err != nil {
		fail(w, 500, errorcode.InternalError)
		return
	}
	respond(w, 200, map[string]any{"object": "response.input_tokens", "input_tokens": count})
}

func (a *app) compact(w http.ResponseWriter, r *http.Request, p provider) {
	var request map[string]json.RawMessage
	if body(r, &request) != nil || !validResponses(request) {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	var input []json.RawMessage
	if json.Unmarshal(request["input"], &input) != nil || len(input) == 0 {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	input = append(input, json.RawMessage(`{"type":"compaction_trigger"}`))
	request["input"], _ = json.Marshal(input)
	request["stream"] = json.RawMessage("true")
	request["store"] = json.RawMessage("false")
	if len(request["instructions"]) == 0 {
		request["instructions"] = json.RawMessage(`""`)
	}
	payload, _ := json.Marshal(request)
	res, err := a.upstreamRequest(r.Context(), p, "/responses", payload)
	if err != nil {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		if res.StatusCode == http.StatusTooManyRequests {
			writeUpstreamLimitError(w, res, time.Now())
			return
		}
		fail(w, res.StatusCode, errorcode.UpstreamError)
		return
	}
	response, err := completedResponse(res.Body)
	if err != nil {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	responseBytes, _ := json.Marshal(response)
	items, _ := response["output"].([]any)
	for _, item := range items {
		if value, ok := item.(map[string]any); ok && value["type"] == "compaction" {
			a.writeToolJSON(w, r, http.StatusOK, responseBytes)
			return
		}
	}
	fail(w, 502, errorcode.UpstreamError)
}

func (a *app) search(w http.ResponseWriter, r *http.Request, p provider) {
	var request struct {
		Query string `json:"query"`
	}
	if body(r, &request) != nil || strings.TrimSpace(request.Query) == "" {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	model := env("BOS_SEARCH_MODEL", "gpt-5.6-sol")
	tool := "web_search"
	if p.Type == "api_key" {
		tool = "web_search_preview"
	}
	payload, _ := json.Marshal(map[string]any{"model": model, "input": request.Query, "stream": false, "tools": []any{map[string]string{"type": tool}}})
	if p.Type == "api_key" {
		copied := r.Clone(r.Context())
		copied.Body = io.NopCloser(bytes.NewReader(payload))
		a.proxyAPI(w, copied, p, "/responses")
		return
	}
	copied := r.Clone(r.Context())
	copied.Body = io.NopCloser(bytes.NewReader(payload))
	a.responses(w, copied, p)
}

// apiMessages selects the Chat bridge only for providers that declare Chat support.
func (a *app) apiMessages(w http.ResponseWriter, r *http.Request, p provider) {
	if p.APIFormat == "openai_chat" || p.APIFormat == "" {
		a.crossProtocolAPI(w, r, p, "messages")
		return
	}
	crossFailure(w, r, "messages", http.StatusBadRequest, errorcode.UnsupportedConversion,
		&gatewayinfra.ConversionError{Field: "endpoint", Reason: "messages conversion requires an openai_chat, anthropic_messages or gemini upstream"})
}
