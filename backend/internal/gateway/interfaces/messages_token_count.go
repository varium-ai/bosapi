package interfaces

import (
	"io"
	"net/http"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/logging"
)

// Chat has no standard token-counting endpoint. Estimate only the prompt that
// the Messages bridge would send, using its existing capability validation.
// This is advisory: O200kBase is not the tokenizer of every compatible model.
func (a *app) countChatMessageTokens(w http.ResponseWriter, r *http.Request, p provider) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestBodyLimit(r)))
	if err != nil {
		crossFailure(w, r, "messages", http.StatusBadRequest, errorcode.InvalidRequest,
			&gatewayinfra.ConversionError{Field: "body", Reason: "request body could not be read within the request limit"})
		return
	}
	if err := validateRelayHeaders(r, p, "messages"); err != nil {
		crossFailure(w, r, "messages", http.StatusBadRequest, errorcode.UnsupportedConversion, err)
		return
	}
	q, err := gatewayinfra.ParseRelayRequest(raw, "messages", p.APIFormat)
	if err != nil {
		crossFailure(w, r, "messages", http.StatusBadRequest, errorcode.UnsupportedConversion, err)
		return
	}
	if model := requestModel(r); model != "" && model != q.Model {
		crossFailure(w, r, "messages", http.StatusBadRequest, errorcode.UnsupportedConversion,
			&gatewayinfra.ConversionError{Field: "model", Reason: "body model must match the authorized request model"})
		return
	}
	payload, _, err := q.Upstream(p.APIFormat)
	if err != nil {
		crossFailure(w, r, "messages", http.StatusBadRequest, errorcode.UnsupportedConversion, err)
		return
	}
	converted, err := gatewayinfra.DecodeProtocolJSON(payload)
	if err != nil {
		nativeError(w, "anthropic_messages", http.StatusInternalServerError, errorcode.InternalError)
		return
	}
	prompt := map[string]any{"messages": converted["messages"]}
	for _, field := range []string{"tools", "tool_choice", "response_format"} {
		if value, exists := converted[field]; exists {
			prompt[field] = value
		}
	}
	count, err := tokenEstimate(prompt)
	if err != nil {
		nativeError(w, "anthropic_messages", http.StatusInternalServerError, errorcode.InternalError)
		return
	}
	count += 4*len(converted["messages"].([]any)) + 2
	w.Header().Set("X-BOS-Token-Count-Estimated", "true")
	w.Header().Set("X-BOS-Token-Count-Tokenizer", "o200k_base")
	logging.Printf(r.Context(), "INFO", "gateway.tokens.estimated", "provider_id=%d model=%s tokenizer=o200k_base input_tokens=%d", p.ID, q.Model, count)
	// Do not observe this estimate as inference usage or charge token quotas.
	respond(w, http.StatusOK, map[string]int{"input_tokens": count})
}
