package interfaces

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
)

const (
	chatResponsePrefix                       = "resp_boschat_"
	chatHistoryTTL                           = 30 * time.Minute
	chatHistoryMaxBytes                      = 1 << 20
	chatHistoryMemoryLimit                   = 32
	chatHistoryReferenceLimit                = 16
	chatHistoryUnavailable    errorcode.Code = "response_store_unavailable"
	chatHistoryMissing        errorcode.Code = "previous_response_not_found"
)

var errChatHistoryMissing = errors.New("response history not found")
var errChatHistoryOversize = errors.New("response history exceeds storage limit")

type chatHistoryEntry struct {
	ProviderID int
	Model      string
	Input      []json.RawMessage
	Response   json.RawMessage
}

type chatHistoryCache struct {
	mu      sync.Mutex
	entries map[string]chatHistoryCacheItem
}

type chatHistoryCacheItem struct {
	sealed  []byte
	expires time.Time
}

func chatResponseID(id string) bool {
	value, ok := strings.CutPrefix(id, chatResponsePrefix)
	return ok && len(value) == 22 && strings.IndexFunc(value, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-')
	}) < 0
}

func chatHistoryKey(key apiKey, id string) string {
	return "bos:gateway:responses-chat:v1:" + digest(fmt.Sprintf("%d:%d:%s:%s", key.OrgID, key.ID, key.Hash, id))
}

func (a *app) chatHistoryCipher() (cipher.AEAD, error) {
	block, err := aes.NewCipher(a.encryptionKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (a *app) saveChatHistory(ctx context.Context, key apiKey, id string, entry chatHistoryEntry) error {
	if key.Hash == "" || !chatResponseID(id) {
		return errChatHistoryMissing
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if len(raw) > chatHistoryMaxBytes {
		return errChatHistoryOversize
	}
	box, err := a.chatHistoryCipher()
	if err != nil {
		return err
	}
	name := chatHistoryKey(key, id)
	nonce := randomBytes(box.NonceSize())
	sealed := box.Seal(nonce, nonce, raw, []byte(name))
	if a.redisClient != nil {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		return a.redisClient.Set(ctx, name, sealed, chatHistoryTTL).Err()
	}
	cache := &a.responsesChatHistory
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := time.Now()
	if cache.entries == nil {
		cache.entries = make(map[string]chatHistoryCacheItem)
	}
	var oldest string
	for k, item := range cache.entries {
		if !item.expires.After(now) {
			delete(cache.entries, k)
		} else if oldest == "" || item.expires.Before(cache.entries[oldest].expires) {
			oldest = k
		}
	}
	if len(cache.entries) >= chatHistoryMemoryLimit {
		delete(cache.entries, oldest)
	}
	cache.entries[name] = chatHistoryCacheItem{sealed: sealed, expires: now.Add(chatHistoryTTL)}
	return nil
}

func (a *app) loadChatHistory(ctx context.Context, key apiKey, id string) (entry chatHistoryEntry, err error) {
	if key.Hash == "" || !chatResponseID(id) {
		return entry, errChatHistoryMissing
	}
	name := chatHistoryKey(key, id)
	var sealed []byte
	if a.redisClient != nil {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		sealed, err = a.redisClient.Get(ctx, name).Bytes()
		if errors.Is(err, redis.Nil) {
			return entry, errChatHistoryMissing
		}
		if err != nil {
			return entry, err
		}
	} else {
		cache := &a.responsesChatHistory
		cache.mu.Lock()
		item := cache.entries[name]
		if item.expires.After(time.Now()) {
			sealed = item.sealed
		} else {
			delete(cache.entries, name)
		}
		cache.mu.Unlock()
		if sealed == nil {
			return entry, errChatHistoryMissing
		}
	}
	box, err := a.chatHistoryCipher()
	if err != nil {
		return entry, err
	}
	size := box.NonceSize()
	if len(sealed) < size+box.Overhead() || len(sealed) > chatHistoryMaxBytes+size+box.Overhead() {
		return entry, errors.New("invalid stored response size")
	}
	raw, err := box.Open(nil, sealed[:size], sealed[size:], []byte(name))
	if err != nil {
		return entry, err
	}
	err = json.Unmarshal(raw, &entry)
	return entry, err
}

func responseLookupID(path string) string {
	id, ok := strings.CutPrefix(path, "responses/")
	if !ok || strings.Contains(id, "/") || !strings.HasPrefix(id, "resp_") {
		return ""
	}
	return id
}

func (a *app) getChatHistory(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	key, _ := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	entry, err := a.loadChatHistory(r.Context(), key, id)
	if errors.Is(err, errChatHistoryMissing) || err == nil && a.databasePolicyDenial(r.Context(), key, entry.ProviderID, entry.Model) != "" {
		recordGatewayFailure(r, "routing", errorcode.NotFound, "response_id", "stored response not found or expired")
		fail(w, http.StatusNotFound, errorcode.NotFound)
		return
	}
	if err != nil {
		recordGatewayFailure(r, "response_read", chatHistoryUnavailable, "response_id", "stored response could not be read")
		fail(w, http.StatusServiceUnavailable, chatHistoryUnavailable)
		return
	}
	respond(w, http.StatusOK, entry.Response)
}

type chatHistoryRequest struct {
	previous string
	store    bool
	input    []json.RawMessage
}

type chatHistoryFailure struct {
	status int
	code   errorcode.Code
	err    error
}

func chatHistoryError(status int, code errorcode.Code, field, reason string) *chatHistoryFailure {
	return &chatHistoryFailure{status, code, &gatewayinfra.ConversionError{Field: field, Reason: reason}}
}

func (a *app) prepareChatHistory(r *http.Request, p provider, raw []byte) ([]byte, chatHistoryRequest, *chatHistoryFailure) {
	var history chatHistoryRequest
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return raw, history, nil
	}
	_ = json.Unmarshal(root["store"], &history.store) // The converter validates the field's type.
	previous := root["previous_response_id"]
	if len(previous) != 0 && json.Unmarshal(previous, &history.previous) != nil {
		return raw, history, nil
	}
	if !history.store && history.previous == "" && !bytes.HasPrefix(root["input"], []byte("[")) {
		return raw, history, nil
	}
	failure := func(status int, code errorcode.Code, field, reason string) ([]byte, chatHistoryRequest, *chatHistoryFailure) {
		return nil, history, chatHistoryError(status, code, field, reason)
	}
	var current []json.RawMessage
	var text string
	switch {
	case len(root["input"]) > 0 && root["input"][0] == '"' && json.Unmarshal(root["input"], &text) == nil:
		current = []json.RawMessage{root["input"]}
	case json.Unmarshal(root["input"], &current) == nil && len(current) > 0:
	default:
		return raw, history, nil // Let the converter reject invalid input.
	}
	key, _ := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	var items, previousOutput []json.RawMessage
	if history.previous != "" {
		entry, err := a.loadChatHistory(r.Context(), key, history.previous)
		if errors.Is(err, errChatHistoryMissing) || err == nil && (entry.ProviderID != p.ID || entry.Model != requestModel(r)) {
			return failure(http.StatusBadRequest, chatHistoryMissing, "previous_response_id", "stored response not found or expired for this channel and model; resend full input history")
		}
		if err != nil {
			return failure(http.StatusServiceUnavailable, chatHistoryUnavailable, "previous_response_id", "stored response could not be read")
		}
		var prior struct{ Output []json.RawMessage }
		if json.Unmarshal(entry.Response, &prior) != nil {
			return failure(http.StatusServiceUnavailable, chatHistoryUnavailable, "previous_response_id", "stored response could not be read")
		}
		previousOutput = prior.Output
		for _, item := range entry.Input {
			// Historic dynamic declarations are not permission to invoke a tool today.
			if chatHistoryItemType(item) != "additional_tools" {
				items = append(items, item)
			}
		}
		for _, item := range prior.Output {
			switch chatHistoryItemType(item) {
			case "message", "reasoning", "function_call":
				items = append(items, item)
			}
		}
	}
	current, resolved, referenceFailure := a.resolveChatItemReferences(r, p, key, current, history.previous, previousOutput)
	if referenceFailure != nil {
		return nil, history, referenceFailure
	}
	if history.previous != "" || resolved {
		current = append(items, current...)
		root["input"], _ = json.Marshal(current)
		delete(root, "previous_response_id")
		raw, _ = json.Marshal(root)
		if int64(len(raw)) > requestBodyLimit(r) {
			return failure(http.StatusBadRequest, errorcode.InvalidRequest, "input", "resumed input exceeds configured request limit")
		}
		// Prior output becomes prompt content and must go through current redaction rules.
		if sanitizer, ok := a.background.SecurityEvents.(SensitiveContentSanitizer); ok {
			masked, changed, err := sanitizer.SanitizeContent(r.Context(), "prompt", raw)
			if err != nil {
				return failure(http.StatusServiceUnavailable, errorcode.SecurityInspectionUnavailable, "input", "resumed input could not be sanitized")
			}
			if changed {
				raw = masked
				var sanitized struct{ Input []json.RawMessage }
				if int64(len(raw)) > requestBodyLimit(r) || json.Unmarshal(raw, &sanitized) != nil || len(sanitized.Input) == 0 {
					return failure(http.StatusServiceUnavailable, errorcode.SecurityInspectionUnavailable, "input", "resumed input could not be sanitized")
				}
				current = sanitized.Input
			}
		}
	}
	if history.store {
		history.input = current
		if encoded, _ := json.Marshal(current); len(encoded) > chatHistoryMaxBytes {
			return failure(http.StatusBadRequest, errorcode.InvalidRequest, "input", "input exceeds the stored history limit")
		}
		if _, err := a.chatHistoryCipher(); err != nil {
			return failure(http.StatusServiceUnavailable, chatHistoryUnavailable, "store", "response storage is unavailable")
		}
	}
	return raw, history, nil
}

func chatItemReferenceResponseID(id string) string {
	kind, suffix, ok := strings.Cut(id, "_")
	end := strings.LastIndexByte(suffix, '_')
	if !ok || end < 0 || (kind != "msg" && kind != "rs" && kind != "fc") || !chatResponseID(suffix[:end]) {
		return ""
	}
	if _, err := strconv.ParseUint(suffix[end+1:], 10, 32); err != nil {
		return ""
	}
	return suffix[:end]
}

func chatItemReferenceID(item json.RawMessage) (string, bool) {
	var header struct {
		Type string
		ID   json.RawMessage
	}
	if json.Unmarshal(item, &header) != nil || header.Type != "item_reference" && (header.Type != "" || header.ID == nil) {
		return "", false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(item, &fields) != nil {
		return "", false
	}
	if header.Type == "item_reference" {
		if len(fields) != 2 || fields["type"] == nil {
			return "", true
		}
	} else if fields["id"] == nil || len(fields) != 1 && (len(fields) != 2 || string(fields["type"]) != "null") {
		return "", false
	}
	var id string
	_ = json.Unmarshal(fields["id"], &id)
	return id, true
}

func (a *app) resolveChatItemReferences(r *http.Request, p provider, key apiKey, items []json.RawMessage, previous string, previousOutput []json.RawMessage) ([]json.RawMessage, bool, *chatHistoryFailure) {
	// Share the storage deadline and read each parent at most once per request.
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	parents := map[string]map[string]json.RawMessage{}
	limit := requestBodyLimit(r)
	var expandedBytes int64
	resolved := false
	result := items[:0]
	for index, item := range items {
		if id, reference := chatItemReferenceID(item); reference {
			field := fmt.Sprintf("input[%d].id", index)
			if id == "" {
				return nil, false, chatHistoryError(http.StatusBadRequest, errorcode.InvalidRequest, field, "expected an item_reference with a string ID")
			}
			missing := func() *chatHistoryFailure {
				return chatHistoryError(http.StatusBadRequest, errorcode.InvalidRequest, field, "stored item not found or expired for this channel and model; resend the original item in input")
			}
			parent := chatItemReferenceResponseID(id)
			if parent == "" {
				return nil, false, missing()
			}
			output, cached := parents[parent]
			if !cached {
				if len(parents) >= chatHistoryReferenceLimit {
					return nil, false, chatHistoryError(http.StatusBadRequest, errorcode.InvalidRequest, field, "too many distinct stored responses referenced in input")
				}
				stored := struct{ Output []json.RawMessage }{Output: previousOutput}
				if parent != previous {
					entry, err := a.loadChatHistory(ctx, key, parent)
					if errors.Is(err, errChatHistoryMissing) || err == nil && (entry.ProviderID != p.ID || entry.Model != requestModel(r)) {
						return nil, false, missing()
					}
					if err != nil || json.Unmarshal(entry.Response, &stored) != nil {
						return nil, false, chatHistoryError(http.StatusServiceUnavailable, chatHistoryUnavailable, field, "stored response could not be read")
					}
				}
				output = indexChatReferenceItems(stored.Output)
				parents[parent] = output
			}
			item = output[id]
			if item == nil {
				return nil, false, missing()
			}
			resolved = true
			if parent == previous {
				continue // The preceding response is already replayed.
			}
		}
		expandedBytes += int64(len(item))
		if expandedBytes > limit {
			return nil, false, chatHistoryError(http.StatusBadRequest, errorcode.InvalidRequest, "input", "resumed input exceeds configured request limit")
		}
		result = append(result, item)
	}
	return result, resolved, nil
}

func indexChatReferenceItems(output []json.RawMessage) map[string]json.RawMessage {
	// Match the delivered ID, since tool filtering can change output positions.
	items := make(map[string]json.RawMessage, len(output))
	for _, candidate := range output {
		var header struct{ ID, Type string }
		if json.Unmarshal(candidate, &header) == nil &&
			(header.Type == "message" || header.Type == "reasoning" || header.Type == "function_call") {
			items[header.ID] = candidate
		}
	}
	return items
}

func chatHistoryItemType(raw json.RawMessage) string {
	var item struct{ Type string }
	_ = json.Unmarshal(raw, &item)
	return item.Type
}

func (a *app) chatHistorySaver(r *http.Request, p provider, q chatHistoryRequest, model string) func([]byte) error {
	if !q.store {
		return nil
	}
	key, _ := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	return func(raw []byte) error {
		var response struct{ ID string }
		if err := json.Unmarshal(raw, &response); err != nil {
			return err
		}
		return a.saveChatHistory(r.Context(), key, response.ID, chatHistoryEntry{ProviderID: p.ID, Model: model, Input: q.input, Response: raw})
	}
}

func chatHistoryWriteFailure(err error) (int, errorcode.Code, *gatewayinfra.ConversionError) {
	if errors.Is(err, errChatHistoryOversize) {
		return http.StatusBadRequest, errorcode.InvalidRequest, &gatewayinfra.ConversionError{Field: "store", Reason: "response exceeds the stored history limit"}
	}
	return http.StatusServiceUnavailable, chatHistoryUnavailable, &gatewayinfra.ConversionError{Field: "store", Reason: "response could not be stored"}
}

// Capture the terminal response after the delivery filter, before it is sent.
// This prevents blocked invocations from reappearing in retrieval or replay.
func (writer *toolBodyWriter) saveTerminalResponse(original []byte, chunks [][]byte) bool {
	if !bytes.HasPrefix(original, []byte("event: response.completed\n")) && !bytes.HasPrefix(original, []byte("event: response.incomplete\n")) {
		return true
	}
	for _, chunk := range chunks {
		_, line, ok := bytes.Cut(chunk, []byte("\ndata: "))
		if !ok {
			continue
		}
		var event struct {
			Type     string
			Response json.RawMessage
		}
		if json.Unmarshal(bytes.TrimSpace(line), &event) == nil && (event.Type == "response.completed" || event.Type == "response.incomplete") && len(event.Response) != 0 {
			writer.chatHistoryErr = writer.saveChatHistory(event.Response)
			return writer.chatHistoryErr == nil
		}
	}
	writer.chatHistoryErr = errors.New("no deliverable terminal response to store")
	return false
}
