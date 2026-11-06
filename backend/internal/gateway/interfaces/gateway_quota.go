package interfaces

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/usagetime"
	userinfra "bosapi/backend/internal/user/infrastructure"
)

type gatewayPersistentKeyRequestKey struct{}
type gatewayQuotaAdmissionRequestKey struct{}

type quotaAdmission struct {
	token      chan struct{}
	references int
	pending    *usageEvent
}

// acquireQuotaAdmission keeps a limited key's next request behind settlement,
// including when the previous response has already been flushed to the client.
func (a *app) acquireQuotaAdmission(ctx context.Context, identity string) (*quotaAdmission, func(), error) {
	a.quotaMu.Lock()
	if a.quotaAdmissions == nil {
		a.quotaAdmissions = make(map[string]*quotaAdmission)
	}
	admission := a.quotaAdmissions[identity]
	if admission == nil {
		admission = &quotaAdmission{token: make(chan struct{}, 1)}
		admission.token <- struct{}{}
		a.quotaAdmissions[identity] = admission
	}
	admission.references++
	a.quotaMu.Unlock()
	release := func(held bool) {
		a.quotaMu.Lock()
		defer a.quotaMu.Unlock()
		if held {
			admission.token <- struct{}{}
		}
		admission.references--
		if admission.references == 0 && admission.pending == nil {
			delete(a.quotaAdmissions, identity)
		}
	}
	select {
	case <-ctx.Done():
		release(false)
		return nil, nil, ctx.Err()
	case <-admission.token:
		return admission, func() { release(true) }, nil
	}
}

func (a *app) gatewayQuotaMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		value, _ := c.Get(gatewayAPIKeyContext)
		key := value.(apiKey)
		if !hasGatewayQuota(key) {
			c.Next()
			return
		}
		// Database IDs survive credential rotation; local IDs may be reused after
		// deletion, so local admissions follow the credential instead.
		identity := "local:" + key.Hash
		if persistent, _ := c.Request.Context().Value(gatewayPersistentKeyRequestKey{}).(bool); persistent {
			identity = "database:" + strconv.Itoa(key.ID)
		}
		admission, release, err := a.acquireQuotaAdmission(c.Request.Context(), identity)
		if err != nil {
			recordGatewayFailure(c.Request, "quota_accounting", errorcode.QuotaAccountingUnavailable, "quota", "request canceled while waiting for previous quota settlement")
			fail(c.Writer, http.StatusServiceUnavailable, errorcode.QuotaAccountingUnavailable)
			c.Abort()
			return
		}
		defer release()
		if !a.prepareQuotaRequest(c, admission) {
			return
		}
		c.Next()
	}
}

func hasGatewayQuota(key apiKey) bool {
	return key.QuotaTokens != nil || key.QuotaAmount != nil ||
		key.DailyQuotaTokens != nil || key.WeeklyQuotaTokens != nil || key.MonthlyQuotaTokens != nil ||
		key.DailyQuotaAmount != nil || key.WeeklyQuotaAmount != nil || key.MonthlyQuotaAmount != nil
}

func (a *app) prepareQuotaRequest(c *gin.Context, admission *quotaAdmission) bool {
	if admission.pending != nil {
		if err := a.settleGatewayQuota(*admission.pending); err != nil {
			recordGatewayFailure(c.Request, "quota_accounting", errorcode.QuotaAccountingUnavailable, "quota", "previous request quota settlement could not be persisted")
			fail(c.Writer, http.StatusServiceUnavailable, errorcode.QuotaAccountingUnavailable)
			c.Abort()
			return false
		}
		admission.pending = nil
	}
	// Authentication can precede a wait for the previous request to finish.
	// Re-read the committed balance and key state after acquiring admission.
	key, found, persistent := a.authenticatedKeyWithSource(c.Request)
	if !found {
		fail(c.Writer, http.StatusUnauthorized, errorcode.Unauthorized)
		c.Abort()
		return false
	}
	if denial := gatewayQuotaDenial(key, time.Now()); denial != nil {
		recordGatewayFailure(c.Request, "quota", errorcode.QuotaExceeded, "quota", denial.reason)
		writeGatewayLimitError(c.Writer, errorcode.QuotaExceeded, denial.reason, denial.retryAfter, denial.limit)
		c.Abort()
		return false
	}
	c.Set(gatewayAPIKeyContext, key)
	ctx := context.WithValue(c.Request.Context(), gatewayAPIKeyRequestKey{}, key)
	ctx = context.WithValue(ctx, gatewayPersistentKeyRequestKey{}, persistent)
	ctx = context.WithValue(ctx, gatewayQuotaAdmissionRequestKey{}, admission)
	c.Request = c.Request.WithContext(ctx)
	return true
}

type quotaDenial struct {
	reason     string
	retryAfter time.Duration
	limit      string
}

// gatewayQuotaDenial uses the same UTC+8 calendar as persisted usage. When
// multiple budgets are exhausted, report the one that blocks retries longest.
func gatewayQuotaDenial(key apiKey, now time.Time) *quotaDenial {
	day, week, month := usagetime.PeriodStarts(now)
	dayReset := day.AddDate(0, 0, 1)
	weekReset := week.AddDate(0, 0, 7)
	monthReset := month.In(usagetime.Location()).AddDate(0, 1, 0)
	var denial *quotaDenial
	add := func(reason, limit string, reset time.Time) {
		retry := time.Duration(0)
		if !reset.IsZero() {
			retry = reset.Sub(now)
		}
		if denial == nil || denial.retryAfter > 0 && (retry == 0 || retry > denial.retryAfter) {
			denial = &quotaDenial{reason: reason, retryAfter: retry, limit: limit}
		}
	}
	tokens := func(reason string, limit *int64, used int64, reset time.Time) {
		if limit != nil && used >= *limit {
			// A zero budget remains exhausted even after a calendar reset.
			if *limit <= 0 {
				reset = time.Time{}
			}
			add(reason, strconv.FormatInt(*limit, 10), reset)
		}
	}
	amount := func(reason string, limit *float64, used float64, reset time.Time) {
		if limit != nil && used >= *limit {
			if *limit <= 0 {
				reset = time.Time{}
			}
			add(reason, strconv.FormatFloat(*limit, 'f', -1, 64), reset)
		}
	}
	tokens("total_tokens", key.QuotaTokens, key.QuotaUsedTokens, time.Time{})
	amount("total_amount", key.QuotaAmount, key.QuotaUsedAmount, time.Time{})
	tokens("daily_tokens", key.DailyQuotaTokens, key.DailyUsedTokens, dayReset)
	amount("daily_amount", key.DailyQuotaAmount, key.DailyUsedAmount, dayReset)
	tokens("weekly_tokens", key.WeeklyQuotaTokens, key.WeeklyUsedTokens, weekReset)
	amount("weekly_amount", key.WeeklyQuotaAmount, key.WeeklyUsedAmount, weekReset)
	tokens("monthly_tokens", key.MonthlyQuotaTokens, key.MonthlyUsedTokens, monthReset)
	amount("monthly_amount", key.MonthlyQuotaAmount, key.MonthlyUsedAmount, monthReset)
	return denial
}

// writeGatewayLimitError keeps QPS and quota failures distinguishable for SDKs.
// Retry-After is a positive, rounded-up number of seconds only when the policy
// resets automatically; total and zero budgets require an administrator change.
func writeGatewayLimitError(w http.ResponseWriter, code errorcode.Code, reason string, retry time.Duration, limit string) {
	w.Header().Set("X-RateLimit-Reason", reason)
	w.Header().Set("X-RateLimit-Remaining", "0")
	if limit != "" {
		w.Header().Set("X-RateLimit-Limit", limit)
	}
	if retry > 0 {
		seconds := retry / time.Second
		if retry%time.Second != 0 {
			seconds++
		}
		w.Header().Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
	}
	respond(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{
		"code": string(code), "message": gatewayErrorMessage(code), "reason": reason, "retryable": retry > 0,
	}})
}

func (a *app) settleGatewayQuota(event usageEvent) error {
	if !event.Persistent {
		return a.settleLocalQuota(event)
	}
	if a.keyRepository == nil {
		return errors.New("gateway key repository unavailable")
	}
	// Client disconnects must not cancel billing, but a database outage must not
	// hold admission indefinitely. Retries reuse the idempotent usage identity.
	ctx, cancel := context.WithTimeout(event.Context, 10*time.Second)
	defer cancel()
	record := gatewayUsageRecord(event)
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if _, err = a.keyRepository.RecordGatewayUsageAndQuota(ctx, record); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return err
}

func gatewayUsageRecord(event usageEvent) userinfra.GatewayUsageRecord {
	return userinfra.GatewayUsageRecord{
		RequestID: event.RequestID, UsageID: event.UsageID, OrganizationID: uint64(event.Key.OrgID), APIKeyID: uint64(event.Key.ID),
		Model: event.Model, Endpoint: event.Endpoint, StatusCode: uint16(max(0, min(event.Status, 65535))),
		ErrorCode:            string(event.ErrorCode),
		InputTokens:          uint64(max(0, event.Metrics.InputTokens)),
		CacheReadTokens:      uint64(max(0, event.Metrics.CacheReadTokens)),
		CacheWriteTokens:     uint64(max(0, event.Metrics.CacheWriteTokens)),
		CacheReadCostAmount:  max(0, event.Metrics.CacheReadCost),
		CacheWriteCostAmount: max(0, event.Metrics.CacheWriteCost),
		OutputTokens:         uint64(max(0, event.Metrics.OutputTokens)),
		TotalTokens:          uint64(max(0, event.Metrics.TotalTokens)),
		CostAmount:           max(0, event.Metrics.Cost),
		ClientIP:             append([]byte(nil), event.ClientIP...), IsStream: event.IsStream,
		FirstTokenMillis: event.FirstTokenMillis,
		DurationMillis:   uint32(max(0, min(int(event.Duration.Milliseconds()), int(^uint32(0))))), CreatedAt: time.Now().UTC(),
	}
}

func (a *app) settleLocalQuota(event usageEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for index, key := range a.data.Keys {
		if key.Hash != event.KeyHash {
			continue
		}
		a.data.Keys[index].QuotaUsedTokens += max(0, event.Metrics.TotalTokens)
		a.data.Keys[index].QuotaUsedAmount += max(0, event.Metrics.Cost)
		if err := a.save(); err != nil {
			a.data.Keys[index] = key
			return err
		}
		return nil
	}
	return errors.New("gateway key unavailable for quota settlement")
}
