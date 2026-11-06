package interfaces

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"bosapi/backend/internal/platform/errorcode"
)

type liveCall struct {
	KeyHash     string
	ProviderID  int
	Model       string
	Attestation string
	Expires     time.Time
}
type liveRequest struct {
	SDP     string          `json:"sdp"`
	Session json.RawMessage `json:"session"`
}

func (q liveRequest) valid() bool {
	if q.SDP == "" || len(q.SDP) > 1<<20 || len(q.Session) == 0 {
		return false
	}
	var session map[string]json.RawMessage
	return json.Unmarshal(q.Session, &session) == nil && session != nil
}

func (a *app) live(w http.ResponseWriter, r *http.Request, p provider) {
	if !providerCapabilityEnabled(p, "realtime") {
		fail(w, http.StatusForbidden, errorcode.ProviderCapabilityUnavailable)
		return
	}
	markRealtimeUsage(r)
	var input liveRequest
	if body(r, &input) != nil || !input.valid() {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	if state := usageState(r); state != nil {
		observeUsageModelJSON(state, input.Session)
	}
	var request *http.Request
	var attestation string
	var err error
	if p.Type == "api_key" {
		var form bytes.Buffer
		writer := multipart.NewWriter(&form)
		if writer.WriteField("sdp", input.SDP) != nil || writer.WriteField("session", string(input.Session)) != nil || writer.Close() != nil {
			fail(w, 500, errorcode.InternalError)
			return
		}
		endpoint, endpointErr := providerEndpoint(p, "/realtime/calls")
		if endpointErr != nil {
			fail(w, 500, errorcode.InternalError)
			return
		}
		request, err = http.NewRequestWithContext(r.Context(), "POST", endpoint, &form)
		if err != nil {
			fail(w, 500, errorcode.InternalError)
			return
		}
		request.Header.Set("Authorization", "Bearer "+p.APIKey)
		request.Header.Set("Content-Type", writer.FormDataContentType())
	} else {
		attestation, err = generateLiveAttestation(r.Context())
		if err != nil {
			fail(w, 503, errorcode.LiveUnavailable)
			return
		}
		payload, _ := json.Marshal(input)
		request, err = http.NewRequestWithContext(r.Context(), "POST", upstream+"/realtime/calls?intent=quicksilver&architecture=avas", bytes.NewReader(payload))
		if err != nil {
			fail(w, 500, errorcode.InternalError)
			return
		}
		request.Header.Set("Authorization", "Bearer "+p.AccessToken)
		request.Header.Set("ChatGPT-Account-ID", p.AccountID)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("x-oai-attestation", attestation)
		request.Header.Set("OpenAI-Alpha", "quicksilver=v2")
		request.Header.Set("Version", codexVersion)
		request.Header.Set("Originator", "codex-tui")
		request.Header.Set("User-Agent", codexUserAgent)
	}
	request.Header.Set("Accept", "application/sdp")
	response, err := a.client.Do(request)
	if err != nil {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		if response.StatusCode == http.StatusTooManyRequests {
			writeUpstreamLimitError(w, response, time.Now())
			return
		}
		fail(w, response.StatusCode, errorcode.UpstreamError)
		return
	}
	callID, ok := liveCallID(response.Header.Get("Location"))
	if !ok {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	sdp, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	keyHash := digest(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	a.mu.Lock()
	if a.liveCalls == nil {
		a.liveCalls = map[string]liveCall{}
	}
	model := ""
	if state := usageState(r); state != nil {
		model = state.model
	}
	a.liveCalls[callID] = liveCall{KeyHash: keyHash, ProviderID: p.ID, Model: model, Attestation: attestation, Expires: time.Now().Add(time.Hour)}
	a.mu.Unlock()
	w.Header().Set("Location", "/v1/live/"+url.PathEscape(callID))
	w.Header().Set("Content-Type", "application/sdp")
	w.WriteHeader(200)
	_, _ = w.Write(sdp)
}

func liveCallID(location string) (string, bool) {
	u, err := url.Parse(location)
	if err != nil {
		return "", false
	}
	id := path.Base(strings.TrimRight(u.Path, "/"))
	if id == "" || id == "." || id == "codex" || len(id) > 128 || strings.ContainsAny(id, "/\\?&#") {
		return "", false
	}
	return id, true
}

func (a *app) liveSideband(w http.ResponseWriter, r *http.Request, p provider, id string) {
	if !providerCapabilityEnabled(p, "realtime") {
		fail(w, http.StatusForbidden, errorcode.ProviderCapabilityUnavailable)
		return
	}
	markRealtimeUsage(r)
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		fail(w, 426, errorcode.UpgradeRequired)
		return
	}
	if _, ok := liveCallID("/" + id); !ok {
		fail(w, 404, errorcode.NotFound)
		return
	}
	keyHash := digest(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	a.mu.Lock()
	call, ok := a.liveCalls[id]
	a.mu.Unlock()
	if !ok || call.KeyHash != keyHash || call.ProviderID != p.ID || time.Now().After(call.Expires) {
		fail(w, 404, errorcode.NotFound)
		return
	}
	if state := usageState(r); state != nil && state.model == "" {
		state.model = call.Model
	}
	headers := make(http.Header)
	if p.Type == "api_key" {
		headers.Set("Authorization", "Bearer "+p.APIKey)
		target, err := providerWebSocketEndpointWithQuery(p, "/realtime", url.Values{"call_id": []string{id}})
		if err != nil {
			fail(w, 502, errorcode.UpstreamError)
			return
		}
		a.bridgeWebSocket(w, r, target, headers)
		return
	}
	headers.Set("Authorization", "Bearer "+p.AccessToken)
	headers.Set("ChatGPT-Account-ID", p.AccountID)
	headers.Set("x-oai-attestation", call.Attestation)
	headers.Set("OpenAI-Alpha", "quicksilver=v2")
	headers.Set("Version", codexVersion)
	headers.Set("Originator", "codex-tui")
	headers.Set("User-Agent", codexUserAgent)
	a.bridgeWebSocket(w, r, "wss://chatgpt.com/backend-api/codex/"+url.PathEscape(id), headers)
}
