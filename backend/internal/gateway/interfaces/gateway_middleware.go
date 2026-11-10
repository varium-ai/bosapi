package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	admindomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/gateway/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/upstreamurl"
)

type errorCodeResponseWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (writer *errorCodeResponseWriter) Write(data []byte) (int, error) {
	original := data
	if writer.body.Len() < 4096 {
		remaining := 4096 - writer.body.Len()
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = writer.body.Write(data)
	}
	return writer.ResponseWriter.Write(original)
}

func (writer *errorCodeResponseWriter) WriteString(value string) (int, error) {
	return writer.Write([]byte(value))
}

func (writer *errorCodeResponseWriter) errorCode() errorcode.Code {
	var envelope struct {
		Error struct {
			Code    json.RawMessage `json:"code"`
			BOSCode string          `json:"bos_code"`
			Type    string          `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(writer.body.Bytes(), &envelope) != nil {
		return ""
	}
	var code string
	if json.Unmarshal(envelope.Error.Code, &code) == nil {
		return errorcode.Code(code)
	}
	if envelope.Error.BOSCode != "" {
		return errorcode.Code(envelope.Error.BOSCode)
	}
	switch envelope.Error.Type {
	case "rate_limit_error":
		if writer.Header().Get("X-RateLimit-Reason") == "upstream_quota" {
			return errorcode.QuotaExceeded
		}
		return errorcode.RateLimited
	case "authentication_error", "permission_error":
		return errorcode.ProviderAuthFailed
	case "overloaded_error":
		return errorcode.ProviderUnavailable
	case "invalid_request_error", "api_error":
		return errorcode.UpstreamError
	}
	return ""
}

func (a *app) gatewayErrorLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		writer, ok := c.Writer.(*errorCodeResponseWriter)
		if !ok {
			writer = &errorCodeResponseWriter{ResponseWriter: c.Writer}
			c.Writer = writer
		}
		c.Next()
		if c.Writer.Status() < http.StatusBadRequest {
			return
		}
		if recorded, _ := c.Get(gatewayUsageRecordedContext); recorded == true {
			return
		}
		key, ok := c.Request.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
		if !ok {
			return
		}
		a.recordEarlyError(c.Request, key, c.Writer.Status(), writer.errorCode())
	}
}

const (
	gatewayAPIKeyContext        = "gateway.api_key"
	gatewayProviderContext      = "gateway.provider"
	gatewayModelContext         = "gateway.model"
	gatewayPathContext          = "gateway.path"
	gatewayRateLimitContext     = "gateway.rate_limit"
	gatewayUsageContext         = "gateway.usage"
	gatewayUsageRecordedContext = "gateway.usage_recorded"
)

type gatewayProviderRequestKey struct{}
type gatewayAPIKeyRequestKey struct{}
type gatewayModelRequestKey struct{}

var gatewayRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func withGatewayProvider(r *http.Request, p provider) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), gatewayProviderRequestKey{}, p))
}

func (a *app) gatewayRequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := httpapi.RequestIDFromContext(c.Request.Context())
		if requestID == "" {
			requestID = c.GetHeader("X-Request-ID")
		}
		if requestID == "" {
			requestID = c.GetHeader("X-Client-Request-ID")
		}
		if !gatewayRequestIDPattern.MatchString(requestID) {
			requestID = "req_" + random(16)
		}
		c.Request.Header.Set("X-Request-ID", requestID)
		clientIP := requestClientIP(c.Request)
		ip := ""
		if clientIP != nil {
			ip = clientIP.String()
		}
		requestContext := httpapi.WithRequestID(c.Request.Context(), requestID)
		requestContext = httpapi.WithLogMetadata(requestContext, httpapi.LogMetadata{RequestID: requestID, ClientIP: ip})
		c.Request = c.Request.WithContext(requestContext)
		c.Header("X-Request-ID", requestID)
		c.Header("X-Client-Request-ID", requestID)
		c.Next()
	}
}

func gatewayLogEndpoint(r *http.Request) string {
	path := gatewayPath(r)
	if metadata, ok := requestGemini(r); ok {
		return "models/:model:" + metadata.Operation
	}
	switch path {
	case "models", "responses", "responses/compact", "responses/input_tokens", "chat/completions", "messages", "messages/count_tokens", "embeddings", "live", "alpha/search", "usage":
		return path
	}
	if strings.HasPrefix(path, "models/") {
		return "models/:model"
	}
	if strings.HasPrefix(path, "live/") {
		return "live/:call_id"
	}
	if strings.HasPrefix(path, "images/") {
		return "images/*"
	}
	return "other"
}

func (a *app) gatewayAccessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayFirstTokenKey{}, &firstTokenTiming{started: start}))
		// Capture errors before authentication too; its failures never reach the
		// usage/error persistence middleware.
		writer := &errorCodeResponseWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), upstreamDiagnosticKey{}, &upstreamDiagnosticState{}))
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayDiagnosticKey{}, &gatewayDiagnosticState{}))
		endpoint := gatewayLogEndpoint(c.Request)
		c.Next()
		keyID, providerID := 0, 0
		if value, ok := c.Get(gatewayAPIKeyContext); ok {
			keyID = value.(apiKey).ID
		}
		if value, ok := c.Get(gatewayProviderContext); ok {
			providerID = value.(provider).ID
		}
		resultStatus, code := gatewayRequestResult(c.Request, c.Writer.Status(), writer.errorCode())
		_, diagnostic := gatewayFailureDiagnostic(c.Request.Context())
		level := "INFO"
		outcome := "success"
		if resultStatus >= 400 {
			level = "ERROR"
			outcome = "error"
		}
		logging.Printf(c.Request.Context(), level, "gateway.request.completed", "method=%s endpoint=%s status=%d result_status=%d error_code=%s duration_ms=%d api_key_id=%d provider_id=%d model=%s operation=%s outcome=%s %s %s", c.Request.Method, endpoint, c.Writer.Status(), resultStatus, code, time.Since(start).Milliseconds(), keyID, providerID, requestModel(c.Request), requestOperation(c.Request), outcome, diagnostic, upstreamDiagnosticSummary(c.Request.Context()))
	}
}

func (a *app) gatewayBodyLimitMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = withRequestBodyLimit(c.Request, maxBytes)
		if maxBytes > 0 && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func (a *app) gatewayAuthenticationMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, keyOK, persistent := a.authenticatedKeyWithSource(c.Request)
		provider, providerOK := a.authenticateKey(c.Request)
		if !keyOK || !providerOK {
			reason := "key_not_found_or_unusable"
			secret := requestSecret(c.Request)
			switch {
			case secret == "":
				reason = "missing_credential"
			case !validGatewayKeyFormat(secret) && !a.databaseKeyCandidate(c.Request):
				reason = "unsupported_key_format"
			case keyOK && !providerOK:
				reason = string(errorcode.ProviderUnavailable)
			}
			logging.Printf(c.Request.Context(), "WARN", "gateway.authentication.failed", "reason=%s", reason)
			fail(c.Writer, http.StatusUnauthorized, errorcode.Unauthorized)
			c.Abort()
			return
		}
		c.Set(gatewayAPIKeyContext, key)
		c.Request = c.Request.WithContext(httpapi.WithLogMetadata(c.Request.Context(), httpapi.LogMetadata{OrganizationID: uint64(key.OrgID), APIKeyMasked: httpapi.MaskAPIKey(key.Prefix)}))
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayAPIKeyRequestKey{}, key))
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayPersistentKeyRequestKey{}, persistent))
		c.Set(gatewayProviderContext, provider)
		c.Request = withGatewayProvider(c.Request, provider)
		c.Next()
	}
}

func (a *app) gatewaySecurityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		keyValue, _ := c.Get(gatewayAPIKeyContext)
		apiKey := keyValue.(apiKey)
		upstreamDisabled := apiKey.SecurityInspectionUpstreamEnabled != nil && !*apiKey.SecurityInspectionUpstreamEnabled
		var policy admindomain.ToolSecurityPolicy
		var err error
		policy, err = a.readToolSecurityPolicy(c.Request.Context())
		if err != nil {
			logging.Event(c.Request.Context(), "ERROR", "gateway.tool_security_policy.read_failed", "could not read tool security mode")
			fail(c.Writer, http.StatusServiceUnavailable, errorcode.SecurityInspectionUnavailable)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayToolSecurityModeKey{}, policy.Mode))
		inspector, ok := a.background.SecurityEvents.(SecurityInspector)
		var payload []byte
		if c.Request.Body != nil && c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			var readErr error
			payload, readErr = io.ReadAll(io.LimitReader(c.Request.Body, requestBodyLimit(c.Request)+1))
			if readErr != nil || int64(len(payload)) > requestBodyLimit(c.Request) {
				if readErr == nil {
					readErr = &http.MaxBytesError{Limit: requestBodyLimit(c.Request)}
				}
				recordRequestBodyFailure(c.Request, readErr)
				fail(c.Writer, http.StatusBadRequest, errorcode.InvalidRequest)
				c.Abort()
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(payload))
		}
		var toolCalls []domain.ToolCall
		if json.Valid(payload) {
			parser := gatewayinfra.NewToolCallParser(toolCallCaller(c.Request, domain.ToolDirectionRequest), nil, requestBodyLimit(c.Request))
			var parseErr error
			toolCalls, parseErr = parser.Parse(payload)
			if parseErr != nil {
				status, code := toolCallErrorMapping(parseErr)
				failure := parseErr.(*domain.ToolCallParseError)
				annotated := withToolCallMetadata(c.Request, domain.ToolCallMetadata{Protocol: failure.Protocol, Direction: failure.Direction})
				action := "observe"
				auditStatus := http.StatusOK
				if policy.Mode == "block" {
					action = "block"
					auditStatus = status
				}
				a.securityEvent(annotated, apiKey, string(errorcode.ToolCallInvalid), action, auditStatus)
				if policy.Mode == "block" {
					fail(c.Writer, status, code)
					c.Abort()
					return
				}
			}
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayToolCallsRequestKey{}, toolCalls))
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayToolAuthorizationRequestKey{}, gatewayinfra.ParseToolAuthorization(payload)))
		if upstreamDisabled {
			c.Next()
			return
		}
		// Request invocations/results are replayed conversation history. Keep
		// them for response correlation, without publishing repeated findings.
		if !ok {
			c.Next()
			return
		}
		requestID := httpapi.RequestIDFromContext(c.Request.Context())
		if requestID == "" {
			requestID = c.GetHeader("X-Request-ID")
		}
		inspection := SecurityInspection{
			RequestID: requestID, OrganizationID: uint64(apiKey.OrgID), APIKeyID: uint64(apiKey.ID),
			Method: c.Request.Method, Path: gatewayPath(c.Request), Model: requestModel(c.Request), Operation: requestOperation(c.Request), Body: payload, ToolCalls: toolCalls,
		}
		allowed := true
		var result SecurityInspectionResult
		var inspectionErr error
		if structured, ok := a.background.SecurityEvents.(StructuredSecurityInspector); ok {
			result, inspectionErr = structured.InspectResult(c.Request.Context(), inspection)
			allowed = result.Allowed
		} else {
			allowed, inspectionErr = inspector.Inspect(c.Request.Context(), inspection)
		}
		if inspectionErr != nil {
			logging.Printf(c.Request.Context(), "ERROR", "gateway.security_inspection.failed", "path=%s err=%v", c.Request.URL.Path, inspectionErr)
			fail(c.Writer, http.StatusServiceUnavailable, errorcode.SecurityInspectionUnavailable)
			c.Abort()
			return
		}
		headerPayload := requestHeaderSecurityPayload(c.Request)
		if !allowed {
			logging.Printf(c.Request.Context(), "ERROR", "gateway.security_inspection.blocked", "path=%s rule_id=%s", c.Request.URL.Path, result.RuleID)
			ruleID := result.RuleID
			if ruleID == "" {
				ruleID = "security_policy"
			}
			a.securityEvent(withSecurityInspection(c.Request, result), apiKey, ruleID, "block", http.StatusForbidden, result.HitCount)
			fail(c.Writer, http.StatusForbidden, errorcode.SecurityBlocked)
			c.Abort()
			return
		}
		if result.RuleID != "" {
			logging.Printf(c.Request.Context(), "WARN", "gateway.security_inspection.observed", "path=%s rule_id=%s action=%s", c.Request.URL.Path, result.RuleID, result.Action)
		}
		if contentInspector, ok := a.background.SecurityEvents.(SecurityContentInspector); ok && len(headerPayload) > 0 && result.RuleID == "" {
			headerResult, headerErr := contentInspector.InspectContent(c.Request.Context(), "prompt", headerPayload)
			if headerErr == nil && headerResult.RuleID != "" {
				logging.Printf(c.Request.Context(), "WARN", "gateway.content_security.observed", "kind=prompt rule_id=%s", headerResult.RuleID)
				result = headerResult
			}
		}
		if contentInspector, ok := a.background.SecurityEvents.(SecurityContentInspector); ok && len(payload) > 0 && result.RuleID == "" {
			contentResult, contentErr := contentInspector.InspectContent(c.Request.Context(), "prompt", payload)
			if contentErr == nil && contentResult.RuleID != "" {
				logging.Printf(c.Request.Context(), "WARN", "gateway.content_security.observed", "kind=prompt rule_id=%s", contentResult.RuleID)
				result = contentResult
			}
		}
		processing := "observed"
		if sanitizer, ok := a.background.SecurityEvents.(SensitiveContentSanitizer); ok && len(payload) > 0 {
			masked, changed, sanitizeErr := sanitizer.SanitizeContent(c.Request.Context(), "prompt", payload)
			if sanitizeErr != nil {
				logging.Printf(c.Request.Context(), "ERROR", "gateway.content_sanitization.failed", "path=%s err=%v", c.Request.URL.Path, sanitizeErr)
				fail(c.Writer, http.StatusServiceUnavailable, errorcode.SecurityInspectionUnavailable)
				if result.RuleID != "" {
					a.securityEvent(withSecurityProcessing(withSecurityInspection(c.Request, result), "not_forwarded"), apiKey, result.RuleID, "observe", http.StatusServiceUnavailable, result.HitCount)
				}
				c.Abort()
				return
			}
			if changed {
				processing = "redacted"
				c.Request.Body = io.NopCloser(bytes.NewReader(masked))
				c.Request.ContentLength = int64(len(masked))
				c.Request.Header.Set("Content-Length", strconv.Itoa(len(masked)))
			}
		}
		if result.RuleID != "" {
			a.securityEvent(withSecurityProcessing(withSecurityInspection(c.Request, result), processing), apiKey, result.RuleID, "observe", http.StatusOK, result.HitCount)
		}
		a.handoffSecurity(c.Request, result)
		c.Next()
	}
}

func (a *app) gatewayModelMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := gatewayPath(c.Request)
		if a.database != nil && c.Request.Method == http.MethodGet && (path == "models" || strings.HasPrefix(path, "models/")) {
			// Discovery evaluates every available provider in the tenant catalog.
			// It must not depend on the default provider chosen for inference.
			c.Next()
			return
		}
		providerValue, _ := c.Get(gatewayProviderContext)
		p := providerValue.(provider)
		model := requestModel(c.Request)
		keyValue, _ := c.Get(gatewayAPIKeyContext)
		apiKey := keyValue.(apiKey)
		if _, native := requestGemini(c.Request); native && (p.APIFormat != "gemini" || len(p.Models) > 0 && !providerSupportsModel(p, model)) {
			selected, ok := a.providerForKeyModelFormat(c.Request.Context(), apiKey, model, "gemini")
			if !ok {
				nativeError(c.Writer, "gemini", http.StatusForbidden, errorcode.ModelNotAllowed)
				c.Abort()
				return
			}
			p = selected
			c.Set(gatewayProviderContext, p)
			c.Request = withGatewayProvider(c.Request, p)
		}
		if a.database != nil && model != "" && !providerSupportsModel(p, model) {
			if selected, ok := a.providerForKeyModel(c.Request.Context(), apiKey, model); ok {
				p = selected
				c.Set(gatewayProviderContext, p)
				c.Request = withGatewayProvider(c.Request, p)
			}
		}
		if denial := a.databasePolicyDenial(c.Request.Context(), apiKey, p.ID, model, p.APIFormat); denial != "" {
			logging.Printf(c.Request.Context(), "WARN", "gateway.policy.denied", "provider_id=%d model=%s reason=%s", p.ID, model, denial)
			fail(c.Writer, http.StatusForbidden, denial)
			c.Abort()
			return
		}
		if len(p.Models) > 0 && model != "" && !providerSupportsModel(p, model) {
			logging.Printf(c.Request.Context(), "WARN", "gateway.model.denied", "provider_id=%d model=%s reason=model_not_allowed", p.ID, model)
			fail(c.Writer, http.StatusForbidden, errorcode.ModelNotAllowed)
			c.Abort()
			return
		}
		c.Set(gatewayModelContext, model)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayModelRequestKey{}, model))
		c.Next()
	}
}

func providerSupportsModel(p provider, model string) bool {
	p = normalizeGeminiProvider(p)
	model = upstreamurl.ModelID(p.APIFormat, model)
	if containsConfiguredModel(p.Models, model) {
		return true
	}
	if p.ModelMapping != nil {
		if target, ok := p.ModelMapping[model]; ok && target != "" {
			return containsConfiguredModel(p.Models, target) || len(p.Models) == 0
		}
	}
	return false
}

// gatewaySessionMiddleware reserves capacity before quota, security and model
// checks, so those operations count as an active request session.
func (a *app) gatewaySessionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, _ := c.Get(gatewayAPIKeyContext)
		apiKey := key.(apiKey)
		path := gatewayPath(c.Request)
		limitKey := RateLimitKey{OrganizationID: uint64(apiKey.OrgID), APIKeyID: uint64(apiKey.ID), Route: path, Model: requestModel(c.Request), Operation: requestOperation(c.Request)}
		logging.Printf(c.Request.Context(), "INFO", "gateway.rate_limit.check", "method=%s path=%s model=%s operation=%s", c.Request.Method, path, limitKey.Model, limitKey.Operation)
		if sessions, ok := a.background.RateLimiter.(domain.SessionLimiter); ok {
			sessionDecision, release, acquireErr := sessions.AcquireSession(c.Request.Context(), limitKey)
			if acquireErr != nil {
				sessionDecision = RateLimitDecision{Allowed: true, Degraded: true}
			}
			if sessionDecision.Degraded {
				c.Header("X-RateLimit-Degraded", "true")
			}
			if !sessionDecision.Allowed {
				retry := sessionDecision.RetryAfter
				if retry <= 0 {
					retry = time.Second
				}
				writeGatewayLimitError(c.Writer, errorcode.RateLimited, string(sessionDecision.Reason), retry, strconv.FormatInt(sessionDecision.Limit, 10))
				c.Abort()
				return
			}
			if release != nil {
				defer release()
			}
		}
		c.Set(gatewayRateLimitContext, limitKey)
		c.Next()
	}
}

func (a *app) gatewayRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, _ := c.Get(gatewayAPIKeyContext)
		apiKey := key.(apiKey)
		path := gatewayPath(c.Request)
		limitKey := RateLimitKey{OrganizationID: uint64(apiKey.OrgID), APIKeyID: uint64(apiKey.ID), Route: path, Model: requestModel(c.Request), Operation: requestOperation(c.Request)}
		logging.Printf(c.Request.Context(), "INFO", "gateway.rate_limit.decision", "method=%s path=%s model=%s operation=%s", c.Request.Method, path, limitKey.Model, limitKey.Operation)
		decision := RateLimitDecision{Allowed: true}
		var err error
		if decider, ok := a.background.RateLimiter.(RateLimitDecider); ok {
			decision, err = decider.Decide(c.Request.Context(), limitKey, time.Now())
		} else {
			decision.Allowed, err = a.background.RateLimiter.Allow(c.Request.Context(), limitKey, time.Now())
		}
		if err != nil {
			logging.Printf(c.Request.Context(), "ERROR", "gateway.rate_limit.failed", "path=%s err=%v", c.Request.URL.Path, err)
			if decision.Degraded {
				c.Header("X-RateLimit-Degraded", "true")
			} else {
				// Availability-first: backend errors bypass this limiter and surface degradation.
				decision.Allowed = true
				decision.Degraded = true
				c.Header("X-RateLimit-Degraded", "true")
			}
		}
		if decision.Degraded {
			c.Header("X-RateLimit-Degraded", "true")
		}
		if !decision.Allowed {
			retry := decision.RetryAfter
			if retry <= 0 {
				retry = time.Second
			}
			limit := ""
			if decision.Limit > 0 {
				limit = strconv.FormatInt(decision.Limit, 10)
			}
			reason := decision.Reason
			if reason == "" {
				reason = domain.RateLimitQPS
			}
			code := errorcode.RateLimited
			if reason.IsQuota() {
				code = errorcode.QuotaExceeded
			}
			logging.Printf(c.Request.Context(), "WARN", "gateway.rate_limit.denied", "method=%s path=%s reason=%s limit=%d retry_after=%s", c.Request.Method, path, reason, decision.Limit, retry)
			writeGatewayLimitError(c.Writer, code, string(reason), retry, limit)
			c.Abort()
			return
		}
		if _, decider := a.background.RateLimiter.(RateLimitDecider); !decider {
			if err := a.background.RateLimiter.Record(c.Request.Context(), limitKey, time.Now()); err != nil {
				logging.Printf(c.Request.Context(), "ERROR", "gateway.rate_limit.record_failed", "path=%s err=%v", path, err)
				fail(c.Writer, http.StatusServiceUnavailable, errorcode.RateLimiterUnavailable)
				c.Abort()
				return
			}
		}
		logging.Printf(c.Request.Context(), "INFO", "gateway.rate_limit.allowed", "method=%s path=%s limit=%d remaining=%d degraded=%t", c.Request.Method, path, decision.Limit, decision.Remaining, decision.Degraded)
		c.Set(gatewayPathContext, path)
		c.Next()
	}
}

func quotaExceeded(key apiKey) bool {
	return gatewayQuotaDenial(key, time.Now()) != nil
}

func (a *app) gatewayUsageMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		writer := &errorCodeResponseWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		state := &usageMetricsState{}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayUsageStateContextKey{}, state))
		providerValue, _ := c.Get(gatewayProviderContext)
		start := time.Now()
		if timing := firstTokenState(c.Request.Context()); timing != nil {
			start = timing.started
		}
		c.Next()
		a.recordRequest(c.Request, providerValue.(provider), c.Writer.Status(), time.Since(start), writer.errorCode())
		c.Set(gatewayUsageRecordedContext, true)
	}
}
