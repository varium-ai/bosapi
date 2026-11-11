// Package gateway runs the local Codex gateway.
package interfaces

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	adminapp "bosapi/backend/internal/admin/application"
	admindomain "bosapi/backend/internal/admin/domain"
	admininfra "bosapi/backend/internal/admin/infrastructure"
	admininterfaces "bosapi/backend/internal/admin/interfaces"
	analyticsapp "bosapi/backend/internal/analytics/application"
	analyticsinfra "bosapi/backend/internal/analytics/infrastructure"
	authDomain "bosapi/backend/internal/auth/domain"
	authinfra "bosapi/backend/internal/auth/infrastructure"
	gatewayapp "bosapi/backend/internal/gateway/application"
	"bosapi/backend/internal/gateway/domain"
	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/database"
	"bosapi/backend/internal/platform/edition"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/server"
	"bosapi/backend/internal/platform/subscription"
	"bosapi/backend/internal/platform/telemetry"
	"bosapi/backend/internal/platform/upstreamurl"
	"bosapi/backend/internal/platform/usagetime"
	userdomain "bosapi/backend/internal/user/domain"
	userinfra "bosapi/backend/internal/user/infrastructure"
)

const clientID = "app_EMoamEEZ73f0CkXaXp7hrann"
const redirectURI = "http://localhost:1455/auth/callback"
const upstream = "https://chatgpt.com/backend-api/codex"

type oauthSession struct {
	Verifier string
	State    string
	Expires  time.Time
}
type completedOAuth struct {
	StateHash  string
	CodeHash   string
	ProviderID int
	Expires    time.Time
}

type usageMetrics struct {
	InputTokens      int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	OutputTokens     int64
	TotalTokens      int64

	Cost           float64
	CacheReadCost  float64
	CacheWriteCost float64
	CostKnown      bool

	Known    bool
	Realtime bool
}

type usageMetricsState struct {
	metrics       usageMetrics
	model         string
	streamBuffer  []byte
	streaming     bool
	streamFailure errorcode.Code
	realtimeTurn  usageMetrics
}

type usageEvent struct {
	CreatedAt        time.Time
	Context          context.Context
	RequestID        string
	UsageID          string
	Key              apiKey
	KeyHash          string
	Model            string
	Status           int
	ErrorCode        errorcode.Code
	Duration         time.Duration
	FirstTokenMillis *uint32
	Metrics          usageMetrics
	Endpoint         string
	IsStream         bool
	ClientIP         []byte
	Persistent       bool
}

type gatewayKeyRepository interface {
	FindGatewayKey(context.Context, string, time.Time) (userdomain.APIKey, bool, error)
	RecordGatewayUsageAndQuota(context.Context, userinfra.GatewayUsageRecord) (bool, error)
}

type app struct {
	usageReporting       *analyticsapp.Service
	usageReportingWorker *analyticsinfra.Worker
	toolSecurityPolicy   func(context.Context) (admindomain.ToolSecurityPolicy, error)
	maxRequestBodyBytes  int64
	mu                   sync.Mutex
	data                 data
	sessions             map[string]oauthSession
	completed            map[string]completedOAuth
	liveCalls            map[string]liveCall
	path                 string
	encryptionKey        []byte
	client               *http.Client
	database             *gorm.DB
	jwtSecret            []byte
	background           BackgroundServices
	portalLoginLock      authDomain.LoginLockStore
	keyExpiryWorker      *userinfra.KeyExpiryWorker
	store                *gatewayinfra.EncryptedStore
	keyRepository        gatewayKeyRepository
	redisClient          redis.UniversalClient
	monitorRedis         *redis.Client
	usageQueue           chan usageEvent
	usageQueueDone       chan struct{}
	usageQueueWG         sync.WaitGroup
	usageQueueDropped    atomic.Uint64
	usageQueueFailed     atomic.Uint64
	quotaMu              sync.Mutex
	quotaAdmissions      map[string]*quotaAdmission
	organizationQuota    organizationQuotaRuntime
	monitorClose         func()

	organizationPendingMu sync.Mutex
	organizationPending   map[uint64]*organizationPendingReceipts
}

// Config is kept as a compatibility alias for the application-layer startup configuration.
type Config = gatewayapp.Config

// LoadConfig is kept as a compatibility alias for application.LoadConfig.
func LoadConfig() (Config, error) { return gatewayapp.LoadConfig() }

// Gateway owns the gateway state and its optional database connection.
type Gateway struct{ app *app }

// New opens storage and loads encrypted gateway data with local asynchronous defaults.
func New(c Config) (*Gateway, error) {
	return NewWithBackgroundServices(c, defaultBackgroundServices())
}

// NewWithBackgroundServices opens storage with replaceable rate-limit and asynchronous services.
func NewWithBackgroundServices(c Config, services BackgroundServices) (*Gateway, error) {
	services = normalizeBackgroundServices(services)
	if c.MaxRequestBodyBytes < 0 {
		return nil, errors.New("max request body bytes must be positive")
	}
	if c.GatewayTimeout < 0 {
		return nil, errors.New("gateway timeout must be positive")
	}
	if c.GatewayTimeout == 0 {
		c.GatewayTimeout = gatewayapp.DefaultGatewayTimeout
	}
	a := &app{maxRequestBodyBytes: c.MaxRequestBodyBytes, store: gatewayinfra.NewEncryptedStore(c.DataFile, c.EncryptionKey), background: services, path: c.DataFile, encryptionKey: c.EncryptionKey, client: &http.Client{Timeout: c.GatewayTimeout}, sessions: map[string]oauthSession{}, completed: map[string]completedOAuth{}, liveCalls: map[string]liveCall{}}
	if c.DatabaseDSN != "" {
		db, err := database.Open(c.DatabaseDSN)
		if err != nil {
			return nil, err
		}
		a.database, a.jwtSecret = db, []byte(c.JWTSigningKey)
		a.keyRepository = userinfra.NewKeyRepository(db)
		rateLimits := admininfra.NewRateLimitRepository(db)
		if _, noop := services.RateLimiter.(gatewayinfra.NoopRateLimiter); noop {
			services.RateLimiter = gatewayinfra.NewLocalRateLimiter(rateLimits)
			a.background = normalizeBackgroundServices(services)
		}
		a.organizationQuota = gatewayinfra.NewOrganizationQuotaCounter(nil, gatewayinfra.NewGormOrganizationQuotaRepository(a.database), "bos:org:quota")
		if c.RedisURL != "" {
			options, err := redis.ParseURL(c.RedisURL)
			if err != nil {
				return nil, err
			}
			a.redisClient = redis.NewClient(options)
			monitorOptions := *options
			monitorOptions.PoolSize, monitorOptions.MinIdleConns, monitorOptions.MaxRetries = 1, 0, -1
			monitorOptions.ContextTimeoutEnabled = true
			monitorOptions.DialTimeout, monitorOptions.ReadTimeout, monitorOptions.WriteTimeout, monitorOptions.PoolTimeout = time.Second, time.Second, time.Second, time.Second
			a.monitorRedis = redis.NewClient(&monitorOptions)
			if err := a.redisClient.Ping(context.Background()).Err(); err != nil {
				return nil, err
			}
			services.RateLimiter = gatewayinfra.NewRedisRateLimiter(a.redisClient, rateLimits, "bos:gateway:rate")
			a.organizationQuota = gatewayinfra.NewOrganizationQuotaCounter(a.redisClient, gatewayinfra.NewGormOrganizationQuotaRepository(a.database), "bos:org:quota")
			a.portalLoginLock = authinfra.NewLoginLockStore(
				a.redisClient,
				"bos:auth:login-lock",
				c.LoginFailureThreshold,
				c.LoginFailureWindow,
				c.LoginLockDuration,
			)
			a.background = normalizeBackgroundServices(services)
			if a.organizationQuota != nil {
				a.organizationQuota.Start()
			}
		}
		if _, noop := services.SecurityEvents.(gatewayinfra.RuleSecurityEventSink); noop {
			services.SecurityEvents = gatewayinfra.RuleSecurityEventSink{Sink: gatewayinfra.NewDatabaseSecurityEventSink(db, 256), Advanced: services.AdvancedSecurity}
			a.background = normalizeBackgroundServices(services)
		}
		a.toolSecurityPolicy = adminapp.NewSystemSettingService(admininfra.NewSystemSettingRepository(db)).GetToolSecurityPolicy
	}
	g := &Gateway{app: a}
	if err := a.load(); err != nil {
		_ = g.Close()
		return nil, err
	}
	a.startUsageQueue()
	if a.database != nil {
		a.keyExpiryWorker = userinfra.NewKeyExpiryWorker(userinfra.NewKeyRepository(a.database))
		a.usageReporting = analyticsapp.New(analyticsinfra.NewRepository(a.database), c.UsageReporting, analyticsinfra.NewHTTPSender(c.UsageReporting.Timeout), analyticsapp.BuildVersion())
		a.usageReportingWorker = analyticsinfra.NewWorker(a.usageReporting)
		if c.UsageReporting.InvalidReason != "" {
			logging.Event(context.Background(), "WARN", "usage_reporting.configuration_disabled", c.UsageReporting.InvalidReason)
		}
	}
	return g, nil
}

func (g *Gateway) Handler() http.Handler         { return g.app.routes() }
func (g *Gateway) CallbackHandler() http.Handler { return http.HandlerFunc(g.app.oauthCallback) }

// Close releases the optional database connection.
func (g *Gateway) Close() error {
	if g.app.usageReportingWorker != nil {
		g.app.usageReportingWorker.Close()
	}
	if g.app.keyExpiryWorker != nil {
		g.app.keyExpiryWorker.Close()
	}
	g.app.stopUsageQueue()
	if g.app.organizationQuota != nil {
		g.app.organizationQuota.Close()
	}
	if sink, ok := g.app.background.SecurityEvents.(interface{ Close() }); ok {
		sink.Close()
	}
	// Flush request logs after producers stop and before closing the database.
	if g.app.monitorClose != nil {
		g.app.monitorClose()
	}
	if g.app.redisClient != nil {
		_ = g.app.redisClient.Close()
	}
	if g.app.monitorRedis != nil {
		_ = g.app.monitorRedis.Close()
	}
	if g.app.database == nil {
		return nil
	}
	db, err := g.app.database.DB()
	if err != nil {
		return err
	}
	return db.Close()
}
func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code errorcode.Code) {
	if requestID := w.Header().Get("X-Request-ID"); requestID != "" {
		w.Header().Set("X-Request-ID", requestID)
	}
	codeValue := string(code)
	respond(w, status, map[string]any{"error": map[string]string{"code": codeValue, "message": gatewayErrorMessage(code)}})
}
func body(r *http.Request, target any) error {
	d := json.NewDecoder(http.MaxBytesReader(nil, r.Body, requestBodyLimit(r)))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing data")
	}
	return nil
}
func (a *app) routes() http.Handler {
	if a.background.RateLimiter == nil || a.background.SecurityEvents == nil {
		a.background = normalizeBackgroundServices(a.background)
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/health", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
	gateway := router.Group("")
	gateway.Use(a.gatewayBodyLimitMiddleware(a.requestBodyLimit()), a.gatewayRequestIDMiddleware(), a.gatewayAccessLogMiddleware(), a.gatewayAuthenticationMiddleware(), a.gatewayErrorLogMiddleware(), a.gatewayGeminiMiddleware(), a.gatewaySessionMiddleware(), a.gatewayQuotaMiddleware(), a.gatewaySecurityMiddleware(), a.gatewayModelMiddleware(), a.gatewayRateLimitMiddleware(), a.gatewayUsageMiddleware())
	gateway.Any("/v1beta/models", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/v1beta/models/*modelAction", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/v1", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/v1/*path", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/models", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/models/*path", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/responses", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/responses/*path", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/chat/completions", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/messages", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/messages/*path", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/embeddings", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/images", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	gateway.Any("/images/*path", func(ctx *gin.Context) { a.gateway(ctx.Writer, ctx.Request) })
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		router.ServeHTTP(w, r)
	})
	if a.database != nil {
		runtime := admininterfaces.RuntimeStatus{Queue: a.queueStatus, UsageReporting: a.usageReporting, SnapshotRedis: a.redisClient}
		runtime.QueueDetails = func() telemetry.Fields {
			return telemetry.Fields{"state": a.queueStatus(), "depth": len(a.usageQueue), "capacity": cap(a.usageQueue), "dropped": a.usageQueueDropped.Load(), "failed": a.usageQueueFailed.Load()}
		}
		if a.monitorRedis != nil {
			runtime.Redis = func(ctx context.Context) error { return a.monitorRedis.Ping(ctx).Err() }
			runtime.RedisInfo = func(ctx context.Context) (string, error) {
				return a.monitorRedis.Info(ctx, "server", "clients", "memory", "stats", "persistence", "replication").Result()
			}
		}
		runtime.RateLimit = func(ctx context.Context) (bool, string) {
			if a.redisClient == nil {
				return true, "local_counter"
			}
			if err := a.redisClient.Ping(ctx).Err(); err != nil {
				return true, string(errorcode.CounterBackendUnavailable)
			}
			return false, "redis_counter"
		}
		if a.organizationQuota != nil {
			runtime.OrganizationQuotaUsage = a.organizationQuota.Usage
		}
		runtime.RateLimitCacheInvalidator = func(ctx context.Context, update admindomain.RateLimitUpdate) {
			if a.organizationQuota == nil {
				return
			}
			for _, organization := range update.Organizations {
				a.organizationQuota.Invalidate(organization.OrganizationID)
				a.organizationQuota.PublishInvalidate(ctx, organization.OrganizationID)
			}
		}
		portal, err := server.NewPortalRouter(a.database, a.jwtSecret, 15*time.Minute, 30*24*time.Hour, a.requestBodyLimit(), a.encryptionKey, fallback, a.portalLoginLock, runtime)
		if err != nil {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "server configuration error", http.StatusInternalServerError)
			})
		}
		if a.monitorClose != nil {
			a.monitorClose()
		}
		a.monitorClose = portal.Close
		return portal.Handler
	}
	return router
}
func (a *app) startOAuth(w http.ResponseWriter) {
	state := random(32)
	verifier := random(64)
	session := random(24)
	v := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI}, "scope": {"openid profile email offline_access"}, "state": {state}, "code_challenge_method": {"S256"}, "id_token_add_organizations": {"true"}, "codex_cli_simplified_flow": {"true"}}
	h := sha256.Sum256([]byte(verifier))
	v.Set("code_challenge", base64.RawURLEncoding.EncodeToString(h[:]))
	a.mu.Lock()
	a.sessions[session] = oauthSession{verifier, state, time.Now().Add(10 * time.Minute)}
	a.mu.Unlock()
	respond(w, 200, map[string]any{"data": map[string]string{"session_id": session, "auth_url": "https://auth.openai.com/oauth/authorize?" + v.Encode()}})
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (a *app) exchange(ctx context.Context, values url.Values) (tokens, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://auth.openai.com/oauth/token", strings.NewReader(values.Encode()))
	if err != nil {
		return tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := a.client.Do(req)
	if err != nil {
		logging.Printf(ctx, "ERROR", "gateway.oauth_token.request_failed", "err=%v", err)
		return tokens{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		logging.Printf(ctx, "WARN", "gateway.oauth_token.bad_status", "status=%d", res.StatusCode)
		return tokens{}, fmt.Errorf("token endpoint returned %d", res.StatusCode)
	}
	var t tokens
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&t); err != nil {
		logging.Printf(ctx, "ERROR", "gateway.oauth_token.decode_failed", "err=%v", err)
		return tokens{}, err
	}
	if t.AccessToken == "" {
		return tokens{}, errors.New("token endpoint returned incomplete credentials")
	}
	return t, nil
}
func accountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Auth struct {
			ID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return claims.Auth.ID
}
func (a *app) finishOAuth(w http.ResponseWriter, r *http.Request) {
	var q struct {
		SessionID string `json:"session_id"`
		Code      string `json:"code"`
		State     string `json:"state"`
	}
	if body(r, &q) != nil || q.Code == "" {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	a.mu.Lock()
	if done, ok := a.completed[q.SessionID]; ok && time.Now().Before(done.Expires) && subtle.ConstantTimeCompare([]byte(digest(q.State)), []byte(done.StateHash)) == 1 && subtle.ConstantTimeCompare([]byte(digest(q.Code)), []byte(done.CodeHash)) == 1 {
		a.mu.Unlock()
		respond(w, 200, map[string]any{"data": map[string]int{"id": done.ProviderID}})
		return
	}
	s, ok := a.sessions[q.SessionID]
	delete(a.sessions, q.SessionID)
	a.mu.Unlock()
	if !ok || time.Now().After(s.Expires) || subtle.ConstantTimeCompare([]byte(q.State), []byte(s.State)) != 1 {
		fail(w, 400, errorcode.InvalidOAuthSession)
		return
	}
	t, err := a.exchange(r.Context(), url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {q.Code}, "redirect_uri": {redirectURI}, "code_verifier": {s.Verifier}})
	if err != nil {
		fail(w, 502, errorcode.OAuthExchangeFailed)
		return
	}
	id := accountID(t.AccessToken)
	if id == "" || t.RefreshToken == "" {
		fail(w, 502, errorcode.AccountIdentityMissing)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	n := nextProviderID(a.data.Providers)
	a.data.Providers = append(a.data.Providers, provider{ID: n, Name: "ChatGPT Codex", Platform: "openai", Type: "oauth", Status: "active", AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, AccountID: id, ExpiresAt: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)})
	if err := a.save(); err != nil {
		a.data.Providers = a.data.Providers[:len(a.data.Providers)-1]
		fail(w, 500, errorcode.StorageFailed)
		return
	}
	if a.completed == nil {
		a.completed = map[string]completedOAuth{}
	}
	a.completed[q.SessionID] = completedOAuth{StateHash: digest(q.State), CodeHash: digest(q.Code), ProviderID: n, Expires: time.Now().Add(10 * time.Minute)}
	respond(w, 200, map[string]any{"data": map[string]int{"id": n}})
}

func (a *app) oauthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	if r.URL.Path != "/auth/callback" || r.Method != "GET" {
		fail(w, 404, errorcode.NotFound)
		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	if code == "" || state == "" {
		oauthPage(w, 400, "Authorization callback is incomplete. Start sign in again.")
		return
	}
	a.mu.Lock()
	var sessionID string
	for id, session := range a.sessions {
		if subtle.ConstantTimeCompare([]byte(state), []byte(session.State)) == 1 {
			sessionID = id
			break
		}
	}
	a.mu.Unlock()
	if sessionID == "" {
		oauthPage(w, 400, "Authorization session expired or was already used. Start sign in again.")
		return
	}
	payload, _ := json.Marshal(map[string]string{"session_id": sessionID, "code": code, "state": state})
	req, err := http.NewRequestWithContext(r.Context(), "POST", "/api/v1/admin/openai/create-from-oauth", bytes.NewReader(payload))
	if err != nil {
		oauthPage(w, 500, "Could not complete authorization. Start sign in again.")
		return
	}
	rec := &recordingWriter{header: make(http.Header)}
	a.finishOAuth(rec, req)
	if rec.status != 200 {
		oauthPage(w, rec.status, "Could not connect the account. Start sign in again.")
		return
	}
	oauthPage(w, 200, "ChatGPT/Codex account connected. Return to the admin page and refresh Providers & Models.")
}
func oauthPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><html><head><meta charset=utf-8><title>Codex authorization</title></head><body><p>%s</p></body></html>", message)
}
func (a *app) listProviders(w http.ResponseWriter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]provider, 0, len(a.data.Providers))
	for _, p := range a.data.Providers {
		p.AccessToken = ""
		p.APIKey = ""
		p.RefreshToken = ""
		p.AccountID = ""
		out = append(out, p)
	}
	respond(w, 200, map[string]any{"data": out})
}
func (a *app) listKeys(w http.ResponseWriter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := append([]apiKey{}, a.data.Keys...)
	for i := range out {
		out[i].Hash = ""
	}
	respond(w, 200, map[string]any{"data": out})
}
func (a *app) createKey(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Name        string   `json:"name"`
		OrgID       int      `json:"org_id"`
		ProviderID  int      `json:"provider_id"`
		IPWhitelist []string `json:"ip_whitelist"`
	}
	if body(r, &q) != nil || len(q.Name) == 0 || len(q.Name) > 100 || len(q.IPWhitelist) > 0 {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.data.Keys) >= edition.MaxAPIKeys {
		fail(w, http.StatusConflict, errorcode.Conflict)
		return
	}
	if len(a.data.Providers) == 0 {
		fail(w, 409, errorcode.ProviderUnavailable)
		return
	}
	providerID := q.ProviderID
	if providerID == 0 {
		providerID = a.data.Providers[0].ID
	}
	found := false
	for _, p := range a.data.Providers {
		if p.ID == providerID && p.Status == "active" {
			found = true
		}
	}
	if !found {
		fail(w, 400, errorcode.InvalidProvider)
		return
	}
	secret := "sk-gw-" + hex.EncodeToString(randomBytes(32))
	id := nextKeyID(a.data.Keys)
	k := apiKey{ID: id, Name: q.Name, OrgID: 1, Prefix: secret[:14], Hash: digest(secret), Status: "active", ProviderID: providerID}
	a.data.Keys = append(a.data.Keys, k)
	if err := a.save(); err != nil {
		a.data.Keys = a.data.Keys[:len(a.data.Keys)-1]
		fail(w, 500, errorcode.StorageFailed)
		return
	}
	respond(w, 201, map[string]any{"data": map[string]any{"id": id, "key": secret}})
}
func (a *app) keyAction(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		fail(w, 404, errorcode.NotFound)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.data.Keys {
		if a.data.Keys[i].ID != id {
			continue
		}
		old := a.data.Keys[i]
		if r.Method == "DELETE" && len(parts) == 1 {
			a.data.Keys = append(a.data.Keys[:i], a.data.Keys[i+1:]...)
			if a.save() != nil {
				a.data.Keys = append(a.data.Keys[:i], append([]apiKey{old}, a.data.Keys[i:]...)...)
				fail(w, 500, errorcode.StorageFailed)
				return
			}
			respond(w, 200, map[string]any{"data": map[string]bool{"ok": true}})
			return
		}
		if r.Method == "PATCH" && len(parts) == 2 && parts[1] == "status" {
			var q struct {
				Status string `json:"status"`
			}
			if body(r, &q) != nil || (q.Status != "active" && q.Status != "disabled") {
				fail(w, 400, errorcode.InvalidRequest)
				return
			}
			a.data.Keys[i].Status = q.Status
			if a.save() != nil {
				a.data.Keys[i] = old
				fail(w, 500, errorcode.StorageFailed)
				return
			}
			respond(w, 200, map[string]any{"data": map[string]bool{"ok": true}})
			return
		}
		if r.Method == "POST" && len(parts) == 2 && parts[1] == "reset" {
			secret := "sk-gw-" + hex.EncodeToString(randomBytes(32))
			a.data.Keys[i].Hash = digest(secret)
			a.data.Keys[i].Prefix = secret[:14]
			if a.save() != nil {
				a.data.Keys[i] = old
				fail(w, 500, errorcode.StorageFailed)
				return
			}
			respond(w, 200, map[string]any{"data": map[string]string{"key": secret}})
			return
		}
		break
	}
	fail(w, 404, errorcode.NotFound)
}
func (a *app) providerAction(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method == "POST" && strings.HasSuffix(path, "/test") {
		a.testProvider(w, r, strings.TrimSuffix(path, "/test"))
		return
	}
	if r.Method == "PUT" {
		a.updateProvider(w, r, path)
		return
	}
	id, err := strconv.Atoi(path)
	if err != nil || r.Method != "DELETE" {
		fail(w, 404, errorcode.NotFound)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, k := range a.data.Keys {
		if k.ProviderID == id {
			fail(w, 409, errorcode.ProviderInUse)
			return
		}
	}
	for i, p := range a.data.Providers {
		if p.ID == id {
			a.data.Providers = append(a.data.Providers[:i], a.data.Providers[i+1:]...)
			if a.save() != nil {
				a.data.Providers = append(a.data.Providers[:i], append([]provider{p}, a.data.Providers[i:]...)...)
				fail(w, 500, errorcode.StorageFailed)
				return
			}
			respond(w, 200, map[string]any{"data": map[string]bool{"ok": true}})
			return
		}
	}
	fail(w, 404, errorcode.NotFound)
}
func (a *app) load() error { return a.encryptedStore().Load(&a.data) }

func (a *app) save() error { return a.encryptedStore().Save(a.data) }

func (a *app) encryptedStore() *gatewayinfra.EncryptedStore {
	if a.store == nil {
		a.store = gatewayinfra.NewEncryptedStore(a.path, a.encryptionKey)
	}
	return a.store
}
func requestSecret(r *http.Request) string {
	secret, _ := requestCredential(r)
	return secret
}

// Header credentials retain precedence. Query keys are a Gemini-only fallback
// and are never copied to the upstream URL by the native adapter.
func requestCredential(r *http.Request) (string, string) {
	if secret := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); secret != "" {
		return secret, "authorization"
	}
	if secret := strings.TrimSpace(r.Header.Get("x-api-key")); secret != "" {
		return secret, "x-api-key"
	}
	if secret := strings.TrimSpace(r.Header.Get("x-goog-api-key")); secret != "" {
		return secret, "x-goog-api-key"
	}
	if geminiNativePath(r) != "" {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if keys := query["key"]; err == nil && len(keys) == 1 {
			if secret := strings.TrimSpace(keys[0]); secret != "" {
				return secret, "query-key"
			}
		}
	}
	return "", "none"
}

func (a *app) authenticateKey(r *http.Request) (provider, bool) {
	if key, ok := a.databaseKey(r); ok {
		return a.providerForKeyModel(r.Context(), key, "")
	}
	secret := requestSecret(r)
	if !validGatewayKeyFormat(secret) {
		return provider{}, false
	}
	hash := digest(secret)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, k := range a.data.Keys {
		if subtle.ConstantTimeCompare([]byte(k.Hash), []byte(hash)) == 1 && keyUsable(k, r) {
			for _, p := range a.data.Providers {
				if p.ID == k.ProviderID && p.Status == "active" {
					return normalizeGeminiProvider(p), true
				}
			}
		}
	}
	return provider{}, false
}

func (a *app) authenticatedKeyHash(r *http.Request) string {
	secret := requestSecret(r)
	if !validGatewayKeyFormat(secret) {
		return ""
	}
	hash := digest(secret)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, key := range a.data.Keys {
		if keyUsable(key, r) && subtle.ConstantTimeCompare([]byte(key.Hash), []byte(hash)) == 1 {
			return hash
		}
	}
	if k, ok := a.databaseKey(r); ok {
		return k.Hash
	}
	return ""
}

func (a *app) authenticatedKey(r *http.Request) (apiKey, bool) {
	key, found, _ := a.authenticatedKeyWithSource(r)
	return key, found
}

func (a *app) authenticatedKeyWithSource(r *http.Request) (apiKey, bool, bool) {
	if key, ok := a.databaseKey(r); ok {
		return key, true, true
	}
	secret := requestSecret(r)
	if !validGatewayKeyFormat(secret) {
		return apiKey{}, false, false
	}
	hash := digest(secret)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, key := range a.data.Keys {
		if keyUsable(key, r) && subtle.ConstantTimeCompare([]byte(key.Hash), []byte(hash)) == 1 {
			return key, true, false
		}
	}
	return apiKey{}, false, false
}

func (a *app) databaseKey(r *http.Request) (apiKey, bool) {
	repo := a.keyRepository
	if repo == nil && a.database != nil {
		repo = userinfra.NewKeyRepository(a.database)
	}
	if repo == nil {
		return apiKey{}, false
	}
	secret := requestSecret(r)
	if secret == "" || len(secret) > 4096 || strings.ContainsAny(secret, "\r\n") {
		return apiKey{}, false
	}
	key, found, err := repo.FindGatewayKey(r.Context(), secret, time.Now())
	if err != nil || !found {
		return apiKey{}, false
	}
	gatewayKey := apiKey{
		AllowedProviderIDs: key.AllowedProviderIDs, AllowedModels: key.AllowedModels,
		ID: int(key.ID), Name: key.Name, OrgID: int(key.OrganizationID), Prefix: key.KeyPrefix, Hash: digest(secret), Status: key.Status,
		SecurityInspectionUpstreamEnabled: &key.SecurityInspectionUpstreamEnabled, SecurityInspectionDownstreamEnabled: &key.SecurityInspectionDownstreamEnabled, ExpiresAt: key.ExpiresAt,
		QuotaAmount: key.QuotaAmount, QuotaTokens: key.QuotaTokens, QuotaUsedAmount: key.QuotaUsedAmount, QuotaUsedTokens: key.QuotaUsedTokens,
		DailyQuotaAmount: key.DailyQuotaAmount, WeeklyQuotaAmount: key.WeeklyQuotaAmount, MonthlyQuotaAmount: key.MonthlyQuotaAmount,
		DailyQuotaTokens: key.DailyQuotaTokens, WeeklyQuotaTokens: key.WeeklyQuotaTokens, MonthlyQuotaTokens: key.MonthlyQuotaTokens,
		DailyUsedAmount: key.DailyUsedAmount, WeeklyUsedAmount: key.WeeklyUsedAmount, MonthlyUsedAmount: key.MonthlyUsedAmount,
		DailyUsedTokens: key.DailyUsedTokens, WeeklyUsedTokens: key.WeeklyUsedTokens, MonthlyUsedTokens: key.MonthlyUsedTokens,
		RateLimit5H: key.RateLimit5H, RateLimit1D: key.RateLimit1D, RateLimit7D: key.RateLimit7D, IPAllowlist: key.IPAllowlist, IPBlocklist: key.IPBlocklist,
	}
	if !keyUsable(gatewayKey, r) {
		return apiKey{}, false
	}
	return gatewayKey, true
}

func (a *app) databaseKeyCandidate(r *http.Request) bool {
	secret := requestSecret(r)
	return secret != "" && len(secret) <= 4096 && !strings.ContainsAny(secret, "\r\n")
}

func (a *app) providerForKeyModel(ctx context.Context, key apiKey, requestedModel string) (provider, bool) {
	return a.providerForKeyModelFormat(ctx, key, requestedModel, "")
}

func (a *app) providerForKeyModelFormat(ctx context.Context, key apiKey, requestedModel, apiFormat string) (provider, bool) {
	if a.database != nil {
		var records []admininfra.ProviderRecord
		query := a.database.WithContext(ctx).Where("status = ? AND schedulable = ?", "active", true).
			Where("EXISTS (SELECT 1 FROM organization_provider_bindings b WHERE b.account_id = accounts.id AND b.organization_id = ? AND b.status = ?)", key.OrgID, "active")
		if len(key.AllowedProviderIDs) > 0 {
			query = query.Where("id IN ?", key.AllowedProviderIDs)
		}
		if err := query.Order("priority DESC, id ASC").Find(&records).Error; err == nil && len(records) > 0 {
			var bindingCount int64
			if err := a.database.WithContext(ctx).Table("organization_provider_bindings").Where("organization_id = ?", key.OrgID).Count(&bindingCount).Error; err != nil {
				return provider{}, false
			}
			type providerBinding struct {
				AccountID      uint64
				Priority       int
				ModelAllowlist string
			}
			var bindings []providerBinding
			if err := a.database.WithContext(ctx).Table("organization_provider_bindings").
				Select("account_id, priority, model_allowlist").
				Where("organization_id = ? AND status = ?", key.OrgID, "active").
				Order("priority DESC, account_id ASC").Find(&bindings).Error; err != nil {
				logging.Printf(ctx, "ERROR", "gateway.provider_binding.lookup_failed", "organization_id=%d err=%v", key.OrgID, err)
				return provider{}, false
			}
			bindingsByAccount := make(map[uint64]providerBinding, len(bindings))
			for _, binding := range bindings {
				bindingsByAccount[binding.AccountID] = binding
			}
			if bindingCount > 0 {
				recordsByID := make(map[uint64]admininfra.ProviderRecord, len(records))
				for _, record := range records {
					recordsByID[record.ID] = record
				}
				ordered := make([]admininfra.ProviderRecord, 0, len(bindings))
				for _, binding := range bindings {
					if record, ok := recordsByID[binding.AccountID]; ok {
						ordered = append(ordered, record)
					}
				}
				records = ordered
			}
			cipher, cipherErr := admininfra.NewCredentialCipher(a.encryptionKey)
			if cipherErr == nil {
				for _, rec := range records {
					if apiFormat != "" && rec.APIFormat != apiFormat {
						continue
					}
					binding := bindingsByAccount[rec.ID]
					var bindingModels []string
					if binding.ModelAllowlist != "" && binding.ModelAllowlist != "null" {
						if json.Unmarshal([]byte(binding.ModelAllowlist), &bindingModels) != nil {
							continue
						}
					}
					apiKey, decErr := cipher.Decrypt(string(rec.CredentialsCiphertext))
					if decErr == nil && apiKey != "" {
						baseURL := strings.TrimRight(rec.BaseURL, "/")
						if baseURL == "" {
							switch rec.APIFormat {
							case "anthropic_messages":
								baseURL = "https://api.anthropic.com"
							case "gemini":
								baseURL = "https://generativelanguage.googleapis.com"
							default:
								baseURL = "https://api.openai.com/v1"
							}
						}
						type modelInfo struct {
							ModelName       string  `gorm:"column:model_name"`
							PriceInput      float64 `gorm:"column:price_input_per_1m"`
							PriceOutput     float64 `gorm:"column:price_output_per_1m"`
							PriceCacheRead  float64 `gorm:"column:price_cache_read_per_1m"`
							PriceCacheWrite float64 `gorm:"column:price_cache_write_per_1m"`
						}
						var modelRows []modelInfo
						var modelNames []string
						var capabilities, pricing map[string]any
						var modelMapping map[string]string
						_ = json.Unmarshal([]byte(rec.CapabilitiesJSON), &capabilities)
						_ = json.Unmarshal([]byte(rec.PricingJSON), &pricing)
						if rec.ModelMappingJSON != "" {
							_ = json.Unmarshal([]byte(rec.ModelMappingJSON), &modelMapping)
						}
						normalizedConfig := normalizeGeminiProvider(provider{APIFormat: rec.APIFormat, ModelMapping: modelMapping, Pricing: pricing})
						modelMapping, pricing = normalizedConfig.ModelMapping, normalizedConfig.Pricing
						if err := a.database.WithContext(ctx).Table("provider_models").
							Select("model_name, price_input_per_1m, price_output_per_1m, price_cache_read_per_1m, price_cache_write_per_1m").
							Where("account_id = ? AND status = ?", rec.ID, "active").
							Find(&modelRows).Error; err != nil {
							continue
						}
						for index := range modelRows {
							modelRows[index].ModelName = upstreamurl.ModelID(rec.APIFormat, modelRows[index].ModelName)
						}
						for _, row := range modelRows {
							modelNames = append(modelNames, row.ModelName)
						}
						if pricing == nil {
							pricing = make(map[string]any)
						}
						modelsMap, ok := pricing["models"].(map[string]any)
						if !ok {
							modelsMap = make(map[string]any)
							pricing["models"] = modelsMap
						}
						for _, row := range modelRows {
							if row.PriceInput > 0 || row.PriceOutput > 0 {
								if _, exists := modelsMap[row.ModelName]; !exists {
									modelsMap[row.ModelName] = map[string]any{
										"input":       row.PriceInput,
										"output":      row.PriceOutput,
										"cache_read":  row.PriceCacheRead,
										"cache_write": row.PriceCacheWrite,
									}
								}
							}
						}
						normalized := normalizeGeminiProvider(provider{APIFormat: rec.APIFormat, Models: modelNames, ModelMapping: modelMapping, Pricing: pricing})
						modelNames, modelMapping, pricing = normalized.Models, normalized.ModelMapping, normalized.Pricing
						if rec.APIFormat == "gemini" {
							bindingModels = normalizeGeminiModels(bindingModels)
						}
						if len(bindingModels) > 0 {
							filtered := modelNames[:0]
							for _, model := range modelNames {
								allowed := containsConfiguredModel(bindingModels, model)
								// Bindings authorize public aliases; retain the
								// upstream catalog model behind an allowed alias.
								for _, publicModel := range bindingModels {
									if modelMapping[publicModel] == model {
										allowed = true
										break
									}
								}
								if allowed {
									filtered = append(filtered, model)
								}
							}
							modelNames = filtered
						}
						effectiveModel := requestedModel
						if target, ok := modelMapping[requestedModel]; ok && target != "" {
							effectiveModel = target
						}
						if requestedModel != "" && !containsConfiguredModel(modelNames, requestedModel) && !containsConfiguredModel(modelNames, effectiveModel) {
							continue
						}
						if denial := a.databasePolicyDenial(ctx, key, int(rec.ID), requestedModel, rec.APIFormat); denial != "" {
							continue
						}
						selected := provider{
							ID:           int(rec.ID),
							Name:         rec.Name,
							Platform:     rec.Platform,
							Type:         "api_key",
							Status:       "active",
							BaseURL:      baseURL,
							APIKey:       apiKey,
							Models:       modelNames,
							Capabilities: capabilities,
							Pricing:      pricing,
							ModelMapping: modelMapping,
							APIFormat:    rec.APIFormat,
						}
						if rec.AccountType == "oauth" {
							credentials, err := subscription.Decode(apiKey, rec.Platform)
							cfg, configErr := subscription.Configuration(rec.Platform)
							if err != nil || configErr != nil || baseURL != cfg.BaseURL || rec.APIFormat != cfg.APIFormat {
								continue
							}
							selected = withSubscriptionCredential(selected, credentials)
						}
						return selected, true
					}
				}
			}
		}
		// A database lookup must never fall through to cached or local providers.
		return provider{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.data.Providers {
		if p.Status == "active" && (key.ProviderID == 0 || p.ID == key.ProviderID) {
			if apiFormat != "" && (p.APIFormat != apiFormat || len(p.Models) > 0 && requestedModel != "" && !providerSupportsModel(normalizeGeminiProvider(p), requestedModel)) {
				continue
			}
			return normalizeGeminiProvider(p), true
		}
	}
	if apiFormat != "" {
		return provider{}, false
	}
	if a.database != nil {
		return provider{
			ID:       0,
			Name:     "BOS Local Gateway",
			Platform: "openai",
			Type:     "unconfigured",
			Status:   "active",
		}, true
	}
	return provider{}, false
}

func (a *app) databasePolicyDenial(ctx context.Context, key apiKey, providerID int, model string, formats ...string) (result errorcode.Code) {
	defer func() {
		if result == "" {
			// A rejected candidate does not fail a later permitted route.
			clearGatewayPolicyDiagnostic(ctx)
		}
	}()
	deny := func(code errorcode.Code, param, reason string) errorcode.Code {
		summary := fmt.Sprintf("failure_stage=provider_policy error_param=%q error_reason=%q", param, reason)
		setGatewayFailureDiagnostic(ctx, code, summary)
		logging.Printf(ctx, "WARN", "gateway.provider_policy.denied", "provider_id=%d error_code=%s %s", providerID, code, summary)
		return code
	}
	if len(key.AllowedProviderIDs) > 0 && !containsUint64(key.AllowedProviderIDs, uint64(providerID)) {
		return deny(errorcode.InvalidProvider, "api_key.allowed_provider_ids", "provider is not permitted by api key policy")
	}
	keyModels := key.AllowedModels
	if len(formats) > 0 && formats[0] == "gemini" {
		keyModels = normalizeGeminiModels(keyModels)
		model = upstreamurl.ModelID("gemini", model)
	}
	if model != "" && len(keyModels) > 0 && !containsConfiguredModel(keyModels, model) {
		return deny(errorcode.ModelNotAllowed, "api_key.allowed_models", "model is not permitted by api key policy")
	}
	if a.database == nil {
		if providerID == 0 {
			return deny(errorcode.InvalidProvider, "provider", "no provider was selected")
		}
		return ""
	}
	if key.OrgID == 0 {
		return deny(errorcode.PolicyNotAllowed, "organization", "api key has no organization")
	}
	var organization struct {
		Status             string
		AllowedProviderIDs string
		AllowedModels      string
	}
	if err := a.database.WithContext(ctx).Table("organizations").Select("status, allowed_provider_ids, allowed_models").Where("id = ?", key.OrgID).First(&organization).Error; err != nil {
		logging.Printf(ctx, "ERROR", "gateway.organization_policy.lookup_failed", "organization_id=%d err=%v", key.OrgID, err)
		return deny(errorcode.PolicyNotAllowed, "organization", "organization policy lookup failed")
	}
	if organization.Status != "active" {
		return deny(errorcode.PolicyNotAllowed, "organization", "organization is not active")
	}
	var bindingCount int64
	if err := a.database.WithContext(ctx).Table("organization_provider_bindings").Where("organization_id = ?", key.OrgID).Count(&bindingCount).Error; err != nil {
		logging.Printf(ctx, "ERROR", "gateway.provider_binding.lookup_failed", "organization_id=%d err=%v", key.OrgID, err)
		return deny(errorcode.PolicyNotAllowed, "provider_binding", "organization provider binding lookup failed")
	}
	if bindingCount > 0 {
		var binding admininfra.ProviderBindingRecord
		if err := a.database.WithContext(ctx).Where("organization_id = ? AND account_id = ? AND status = ?", key.OrgID, providerID, "active").First(&binding).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return deny(errorcode.InvalidProvider, "provider_binding", "selected provider has no active binding to this organization")
			}
			logging.Printf(ctx, "ERROR", "gateway.provider_binding.lookup_failed", "organization_id=%d provider_id=%d err=%v", key.OrgID, providerID, err)
			return deny(errorcode.InvalidProvider, "provider_binding", "selected provider binding lookup failed")
		}
		var models []string
		if binding.ModelAllowlist != "" && json.Unmarshal([]byte(binding.ModelAllowlist), &models) != nil {
			return deny(errorcode.PolicyNotAllowed, "provider_binding.model_allowlist", "provider binding model allowlist is invalid JSON")
		}
		if len(formats) > 0 && formats[0] == "gemini" {
			models = normalizeGeminiModels(models)
			model = upstreamurl.ModelID("gemini", model)
		}
		// Binding model allowlists contain exact IDs; unlike legacy organization
		// allowlists, '*' is not a wildcard here (matching catalog discovery).
		if model != "" && len(models) > 0 && !containsConfiguredModel(models, model) {
			return deny(errorcode.ModelNotAllowed, "model", "model is not in the provider binding allowlist")
		}
		return ""
	}
	// A public channel requires an explicit active organization binding.
	return deny(errorcode.InvalidProvider, "provider_binding", "selected provider has no active binding to this organization")
}

func organizationPolicyDenial(status string, providerIDs []uint64, models []string, providerID int, model string) errorcode.Code {
	if status != "active" {
		return errorcode.PolicyNotAllowed
	}
	if providerID == 0 || (len(providerIDs) > 0 && !containsUint64(providerIDs, uint64(providerID))) {
		return errorcode.InvalidProvider
	}
	if !organizationModelAllowed(models, model) {
		return errorcode.ModelNotAllowed
	}
	return ""
}

func organizationModelAllowed(models []string, model string) bool {
	return model == "" || len(models) == 0 || containsConfiguredModel(models, "*") || containsConfiguredModel(models, model)
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validGatewayKeyFormat(secret string) bool {
	return (strings.HasPrefix(secret, "sk-gw-") || strings.HasPrefix(secret, "bos_live_")) && len(secret) <= 128
}

func keyUsable(key apiKey, r *http.Request) bool {
	if key.Status != "active" || key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now()) {
		return false
	}
	clientIP := requestClientIP(r)
	for _, blocked := range key.IPBlocklist {
		if ipMatches(clientIP, blocked) {
			return false
		}
	}
	if len(key.IPAllowlist) > 0 {
		allowed := false
		for _, entry := range key.IPAllowlist {
			if ipMatches(clientIP, entry) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

func requestClientIP(r *http.Request) net.IP {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		return net.ParseIP(forwarded)
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return net.ParseIP(host)
	}
	return net.ParseIP(strings.TrimSpace(r.RemoteAddr))
}

func ipMatches(ip net.IP, entry string) bool {
	if ip == nil {
		return false
	}
	entry = strings.TrimSpace(entry)
	if parsed := net.ParseIP(entry); parsed != nil {
		return parsed.Equal(ip)
	}
	_, network, err := net.ParseCIDR(entry)
	return err == nil && network.Contains(ip)
}

func gatewayPath(r *http.Request) string {
	requestPath := r.URL.Path
	if native := geminiNativePath(r); native != "" {
		requestPath = native
	}
	path := strings.TrimPrefix(requestPath, "/")
	path = strings.TrimPrefix(path, "v1beta/")
	for strings.HasPrefix(path, "v1/") {
		path = strings.TrimPrefix(path, "v1/")
	}
	path = strings.TrimSuffix(path, "/")
	return path
}

func requestModel(r *http.Request) string {
	if model, ok := r.Context().Value(gatewayModelRequestKey{}).(string); ok {
		return model
	}
	if geminiNativePath(r) != "" {
		metadata, ok := requestGemini(r)
		if ok {
			return metadata.Model
		}
		return ""
	}
	if value := r.URL.Query().Get("model"); value != "" {
		return value
	}
	if r.Body == nil || r.Method != http.MethodPost {
		return ""
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, requestBodyLimit(r)))
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(bytes.NewReader(payload))
	var fields struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(payload, &fields) != nil {
		return ""
	}
	return fields.Model
}

func (a *app) securityEvent(r *http.Request, key apiKey, ruleID, action string, status int, counts ...uint32) {
	if a.background.SecurityEvents == nil {
		return
	}
	requestID := httpapi.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = r.Header.Get("X-Request-ID")
	}
	hitCount := uint32(1)
	if len(counts) > 0 && counts[0] > 0 {
		hitCount = counts[0]
	}
	var toolMetadata *ToolCallMetadata
	if metadata, ok := r.Context().Value(gatewayToolMetadataRequestKey{}).(ToolCallMetadata); ok {
		toolMetadata = &metadata
	}
	match, _ := r.Context().Value(gatewayRuleMatchRequestKey{}).(domain.SecurityRuleMatch)
	processing, _ := r.Context().Value(gatewaySecurityProcessingRequestKey{}).(string)
	if err := a.background.SecurityEvents.Publish(r.Context(), SecurityEvent{
		RequestID: requestID, APIKeyID: uint64(key.ID), OrganizationID: uint64(key.OrgID),
		ClientIP: clientIPBytes(requestClientIP(r)),
		RuleID:   ruleID, Action: action, HitCount: hitCount, HTTPStatus: status, CreatedAt: time.Now(),
		ToolCall: toolMetadata, RuleVersion: match.Version, RuleScope: match.Scope, RuleSensitivity: match.Sensitivity,
		RuleName: match.RuleName, Evidence: match.Evidence, ProcessingResult: processing,
	}); err != nil {
		logging.Printf(r.Context(), "ERROR", "gateway.security_event.publish_failed", "err=%v", err)
	}
}

func (a *app) handoffSecurity(r *http.Request, result SecurityInspectionResult) {
	if len(result.Signals) == 0 {
		return
	}
	logging.Event(r.Context(), "INFO", "gateway.security_triggers", "signals="+strings.Join(result.Signals, ","))
}

func (a *app) inspectModelContent(r *http.Request, kind string, payload []byte) {
	if securityInspectionDisabled(r, domain.ToolDirectionResponse) {
		return
	}
	if len(payload) == 0 || len(payload) > 1<<20 {
		return
	}
	inspector, ok := a.background.SecurityEvents.(SecurityContentInspector)
	if !ok {
		return
	}
	result, err := inspector.InspectContent(r.Context(), kind, payload)
	if err != nil {
		logging.Printf(r.Context(), "ERROR", "gateway.content_security.inspection_failed", "kind=%s err=%v", kind, err)
		return
	}
	if result.RuleID == "" {
		return
	}
	logging.Printf(r.Context(), "INFO", "gateway.content_security.observed", "kind=%s rule_id=%s", kind, result.RuleID)
	keyValue, ok := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	if ok {
		a.securityEvent(withSecurityInspection(r, result), keyValue, result.RuleID, "observe", http.StatusOK, result.HitCount)
	}
}

func clientIPBytes(ip net.IP) []byte {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return append([]byte(nil), v4...)
	}
	return append([]byte(nil), ip.To16()...)
}

func (a *app) recordRequest(r *http.Request, provider provider, status int, duration time.Duration, responseCode errorcode.Code) {
	path := gatewayPath(r)
	if path == "usage" || r.Method == http.MethodGet && status < http.StatusBadRequest && (path == "models" || strings.HasPrefix(path, "models/")) {
		return
	}
	key, authenticated := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	if !authenticated {
		return
	}
	keyHash := key.Hash
	model := requestModel(r)
	if model == "" {
		model, _ = r.Context().Value(gatewayModelRequestKey{}).(string)
	}
	state := usageState(r)
	status, responseCode = gatewayRequestResult(r, status, responseCode)
	if model == "" && state != nil {
		model = state.model
	}
	if model == "" {
		model = "unknown"
	}
	metrics, _ := r.Context().Value(gatewayUsageContextKey{}).(usageMetrics)
	if state != nil {
		metrics = state.metrics
	}
	if metrics.Known && !metrics.CostKnown {
		breakdown := providerCostBreakdown(provider, model, metrics)
		metrics.Cost = breakdown.TotalCost
		metrics.CacheReadCost = breakdown.CacheReadCost
		metrics.CacheWriteCost = breakdown.CacheWriteCost
	}
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "req_" + random(16)
	}
	logMetadata := httpapi.LogMetadataFromContext(r.Context())
	logMetadata.RequestID = requestID
	logMetadata.OrganizationID = uint64(key.OrgID)
	logMetadata.APIKeyMasked = httpapi.MaskAPIKey(key.Prefix)
	if clientIP := requestClientIP(r); clientIP != nil {
		logMetadata.ClientIP = clientIP.String()
	}
	logContext := httpapi.WithRequestID(context.Background(), requestID)
	logContext = httpapi.WithLogMetadata(logContext, logMetadata)
	endpoint := path
	if len(endpoint) > 128 {
		endpoint = endpoint[:128]
	}
	isStream := state != nil && state.streaming || strings.HasSuffix(gatewayPath(r), ":streamGenerateContent") || metrics.Realtime || strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
	persistent, _ := r.Context().Value(gatewayPersistentKeyRequestKey{}).(bool)
	event := usageEvent{CreatedAt: time.Now().UTC(), Context: logContext, RequestID: requestID, UsageID: "usage_" + random(16), Key: key, KeyHash: keyHash, Model: model, Status: status, ErrorCode: responseCode, Duration: duration, Metrics: metrics, Endpoint: endpoint, IsStream: isStream, ClientIP: clientIPBytes(requestClientIP(r)), Persistent: persistent}
	event.FirstTokenMillis = firstTokenState(r.Context()).elapsedMillis()
	if err := a.settleGatewayQuota(event); err != nil {
		a.usageQueueFailed.Add(1)
		logging.Printf(logContext, "ERROR", "gateway.quota.persist_failed", "api_key_id=%d err=%v", key.ID, err)
		if admission, ok := r.Context().Value(gatewayQuotaAdmissionRequestKey{}).(*quotaAdmission); ok && admission != nil {
			admission.pending = &event
		}
		if event.Persistent && key.OrgID > 0 && a.organizationQuota != nil {
			a.retainOrganizationReceipt(event)
		}
	}
	if metrics.Known {
		logging.Printf(logContext, "INFO", "gateway.usage.recorded", "api_key_id=%d model=%s input_tokens=%d output_tokens=%d total_tokens=%d cost=%.8f", key.ID, model, metrics.InputTokens, metrics.OutputTokens, metrics.TotalTokens, metrics.Cost)
	} else {
		logging.Printf(logContext, "INFO", "gateway.usage.recorded", "api_key_id=%d model=%s tokens=unknown", key.ID, model)
	}
	select {
	case a.usageQueue <- event:
	default:
		a.usageQueueDropped.Add(1)
		logging.Printf(logContext, "WARN", "gateway.usage.queue_full", "model=%s dropped=%d", model, a.usageQueueDropped.Load())
	}
}

func (a *app) recordEarlyError(r *http.Request, key apiKey, status int, responseCode errorcode.Code) {
	if a.keyRepository == nil {
		return
	}
	requestID := httpapi.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = r.Header.Get("X-Request-ID")
	}
	endpoint := gatewayPath(r)
	if len(endpoint) > 128 {
		endpoint = endpoint[:128]
	}
	model := requestModel(r)
	if model == "" {
		model = "unknown"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := a.keyRepository.RecordGatewayUsageAndQuota(ctx, userinfra.GatewayUsageRecord{
		RequestID: requestID, UsageID: "usage_" + random(16), OrganizationID: uint64(key.OrgID), APIKeyID: uint64(key.ID),
		Model: model, Endpoint: endpoint, StatusCode: uint16(max(0, min(status, 65535))), ErrorCode: string(responseCode),
		ClientIP: clientIPBytes(requestClientIP(r)), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		logging.Printf(r.Context(), "ERROR", "gateway.error_log.persist_failed", "api_key_id=%d err=%v", key.ID, err)
	}
}

func providerCost(p provider, model string, metrics usageMetrics) float64 {
	return providerCostBreakdown(p, model, metrics).TotalCost
}

func providerPricing(p provider, model string) map[string]any {
	pricing := p.Pricing
	if modelPricing, ok := pricing["models"].(map[string]any); ok {
		if selected, ok := modelPricing[model].(map[string]any); ok {
			return selected
		}
		if target, ok := p.ModelMapping[model]; ok && target != "" {
			if selectedTarget, ok := modelPricing[target].(map[string]any); ok {
				return selectedTarget
			}
		}
	}
	return pricing
}

type usageCostBreakdown struct {
	InputCost      float64
	CacheReadCost  float64
	CacheWriteCost float64
	OutputCost     float64
	TotalCost      float64
}

func providerCostBreakdown(p provider, model string, metrics usageMetrics) usageCostBreakdown {
	pricing := providerPricing(p, model)

	input := max(0, numberFloat(pricing["input"]))
	output := max(0, numberFloat(pricing["output"]))
	readPrice, writePrice := input, input
	if value, ok := pricing["cache_read"]; ok {
		readPrice = max(0, numberFloat(value))
	}
	if value, ok := pricing["cache_write"]; ok {
		writePrice = max(0, numberFloat(value))
	}
	inputTokens := max(0, metrics.InputTokens)
	readTokens := max(0, metrics.CacheReadTokens)
	writeTokens := max(0, metrics.CacheWriteTokens)
	regularTokens := max(0, inputTokens-readTokens-writeTokens)
	outputTokens := max(0, metrics.OutputTokens)
	breakdown := usageCostBreakdown{
		InputCost:      float64(regularTokens) * input / 1_000_000,
		CacheReadCost:  float64(readTokens) * readPrice / 1_000_000,
		CacheWriteCost: float64(writeTokens) * writePrice / 1_000_000,
		OutputCost:     float64(outputTokens) * output / 1_000_000,
	}
	breakdown.TotalCost = (float64(regularTokens)*input + float64(readTokens)*readPrice + float64(writeTokens)*writePrice + float64(outputTokens)*output) / 1_000_000
	return breakdown
}
func (a *app) startUsageQueue() {
	a.usageQueue = make(chan usageEvent, 1024)
	a.usageQueueDone = make(chan struct{})
	a.usageQueueWG.Add(1)
	go func() {
		defer a.usageQueueWG.Done()
		for {
			select {
			case event := <-a.usageQueue:
				if event.KeyHash != "" || event.Key.ID > 0 {
					a.processUsageEvent(event)
				}
			case <-a.usageQueueDone:
				for {
					select {
					case event := <-a.usageQueue:
						a.processUsageEvent(event)
					default:
						return
					}
				}
			}
		}
	}()
}

func (a *app) queueStatus() string {
	if a.usageQueue == nil || a.usageQueueDone == nil {
		return "unavailable"
	}
	select {
	case <-a.usageQueueDone:
		return "draining"
	default:
	}
	return "running"
}

func (a *app) stopUsageQueue() {
	if a.usageQueueDone == nil {
		return
	}
	close(a.usageQueueDone)
	a.usageQueueWG.Wait()
}

func (a *app) processUsageEvent(event usageEvent) {
	a.mu.Lock()
	if a.data.Usage == nil {
		a.data.Usage = make(map[string]usage)
	}
	current := a.data.Usage[event.KeyHash]
	if current.Models == nil {
		current.Models = make(map[string]modelUsage)
	}
	if current.Daily == nil {
		current.Daily = make(map[string]dailyUsage)
	}
	current.Requests++
	current.DurationMS += event.Duration.Milliseconds()
	modelStats := current.Models[event.Model]
	modelStats.Requests++
	if event.Status >= 200 && event.Status < 400 {
		current.Successes++
		modelStats.Successes++
	} else {
		current.Errors++
		modelStats.Errors++
	}
	current.InputTokens += event.Metrics.InputTokens
	current.OutputTokens += event.Metrics.OutputTokens
	current.Cost += event.Metrics.Cost
	modelStats.InputTokens += event.Metrics.InputTokens
	modelStats.OutputTokens += event.Metrics.OutputTokens
	modelStats.TotalTokens += event.Metrics.TotalTokens
	modelStats.Cost += event.Metrics.Cost
	current.Models[event.Model] = modelStats
	day := time.Now().In(usagetime.Location()).Format("2006-01-02")
	daily := current.Daily[day]
	daily.Requests++
	daily.DurationMS += event.Duration.Milliseconds()
	daily.InputTokens += event.Metrics.InputTokens
	daily.OutputTokens += event.Metrics.OutputTokens
	daily.Cost += event.Metrics.Cost
	if event.Status >= 200 && event.Status < 400 {
		daily.Successes++
	} else {
		daily.Errors++
	}
	current.Daily[day] = daily
	a.data.Usage[event.KeyHash] = current
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		err = a.save()
		if err == nil {
			break
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 25 * time.Millisecond)
		}
	}
	a.mu.Unlock()
	if err != nil {
		a.usageQueueFailed.Add(1)
		logging.Printf(event.Context, "ERROR", "gateway.usage.persist_failed", "attempts=3 failed=%d err=%v", a.usageQueueFailed.Load(), err)
	}
}

type gatewayUsageContextKey struct{}

func withUsageMetrics(r *http.Request, metrics usageMetrics) *http.Request {
	if state, ok := r.Context().Value(gatewayUsageStateContextKey{}).(*usageMetricsState); ok {
		state.metrics = metrics
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), gatewayUsageContextKey{}, metrics))
}

type gatewayUsageStateContextKey struct{}

func usageState(r *http.Request) *usageMetricsState {
	if state, ok := r.Context().Value(gatewayUsageStateContextKey{}).(*usageMetricsState); ok {
		return state
	}
	return nil
}

func extractUsage(value map[string]any) usageMetrics {
	if usage, ok := value["usageMetadata"].(map[string]any); ok {
		input := numberValue(usage["promptTokenCount"])
		output := numberValue(usage["candidatesTokenCount"]) + numberValue(usage["thoughtsTokenCount"])
		return usageMetrics{Known: true, InputTokens: input, OutputTokens: output, TotalTokens: input + output, CacheReadTokens: numberValue(usage["cachedContentTokenCount"])}
	}
	if usage, ok := value["usage"].(map[string]any); ok {
		return extractUsageFields(usage)
	}
	for _, field := range []string{"response", "data", "message"} {
		if nested, ok := value[field].(map[string]any); ok {
			if metric := extractUsage(nested); metric.Known {
				return metric
			}
		}
	}
	return usageMetrics{}
}

func extractUsageFields(usage map[string]any) usageMetrics {
	metric := usageMetrics{Known: true}
	metric.InputTokens = numberValue(usage["input_tokens"])
	if metric.InputTokens == 0 {
		metric.InputTokens = numberValue(usage["prompt_tokens"])
	}
	metric.OutputTokens = numberValue(usage["output_tokens"])
	if metric.OutputTokens == 0 {
		metric.OutputTokens = numberValue(usage["completion_tokens"])
	}
	metric.CacheReadTokens = numberValue(usage["cache_read_input_tokens"])
	metric.CacheWriteTokens = numberValue(usage["cache_creation_input_tokens"])
	metric.InputTokens += metric.CacheReadTokens + metric.CacheWriteTokens
	// DeepSeek reports cache hits and misses in OpenAI-compatible usage.
	if hit := numberValue(usage["prompt_cache_hit_tokens"]); hit > metric.CacheReadTokens {
		metric.CacheReadTokens = hit
	}
	if metric.InputTokens == 0 {
		if miss := numberValue(usage["prompt_cache_miss_tokens"]); miss > 0 {
			metric.InputTokens = metric.CacheReadTokens + miss
		}
	}
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		if cached := numberValue(details["cached_tokens"]); cached > metric.CacheReadTokens {
			metric.CacheReadTokens = cached
		}
	}
	if details, ok := usage["input_tokens_details"].(map[string]any); ok {
		if cached := numberValue(details["cached_tokens"]); cached > metric.CacheReadTokens {
			metric.CacheReadTokens = cached
		}
	}
	if metric.CacheReadTokens > metric.InputTokens {
		metric.CacheReadTokens = metric.InputTokens
	}
	metric.TotalTokens = numberValue(usage["total_tokens"])
	_, hasInput := usage["input_tokens"]
	_, hasPrompt := usage["prompt_tokens"]
	_, hasOutput := usage["output_tokens"]
	_, hasCompletion := usage["completion_tokens"]
	if hasInput || hasPrompt || hasOutput || hasCompletion || metric.TotalTokens == 0 {
		metric.TotalTokens = metric.InputTokens + metric.OutputTokens
	}
	if _, ok := usage["cost"]; ok {
		metric.Cost = numberFloat(usage["cost"])
		metric.CostKnown = true
	}
	return metric
}

func usageHasTokens(metric usageMetrics) bool {
	return metric.InputTokens > 0 || metric.OutputTokens > 0 || metric.TotalTokens > 0
}

func addUsageTotals(destination *usageMetrics, source usageMetrics) {
	if destination == nil || !usageHasTokens(source) {
		return
	}
	destination.InputTokens += source.InputTokens
	destination.CacheReadTokens += source.CacheReadTokens
	destination.CacheWriteTokens += source.CacheWriteTokens
	destination.OutputTokens += source.OutputTokens
	destination.TotalTokens += source.TotalTokens
	destination.Cost += source.Cost
	destination.CostKnown = destination.CostKnown || source.CostKnown
	destination.Known = true
	if destination.TotalTokens == 0 {
		destination.TotalTokens = destination.InputTokens + destination.OutputTokens
	}
}

func mergeUsageNonZero(destination *usageMetrics, source usageMetrics) {
	if destination == nil {
		return
	}
	if source.InputTokens > 0 {
		destination.InputTokens = source.InputTokens
	}
	if source.CacheReadTokens > 0 {
		destination.CacheReadTokens = source.CacheReadTokens
	}
	if source.CacheWriteTokens > 0 {
		destination.CacheWriteTokens = source.CacheWriteTokens
	}
	if source.OutputTokens > 0 {
		destination.OutputTokens = source.OutputTokens
	}
	if source.TotalTokens > 0 {
		destination.TotalTokens = source.TotalTokens
	}
	if source.CostKnown {
		destination.Cost = source.Cost
		destination.CostKnown = true
	} else if source.Cost != 0 {
		destination.Cost = source.Cost
	}
	destination.Known = destination.Known || source.Known
	if destination.TotalTokens == 0 {
		destination.TotalTokens = destination.InputTokens + destination.OutputTokens
	}
}

func usageEventIsTerminal(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
		return true
	default:
		return false
	}
}

func observeUsageMetric(state *usageMetricsState, metric usageMetrics, eventType string) {
	if state == nil || !metric.Known {
		return
	}
	metric.Realtime = state.metrics.Realtime
	if state.metrics.Realtime {
		if usageEventIsTerminal(eventType) {
			turn := metric
			if !usageHasTokens(turn) {
				turn = state.realtimeTurn
			}
			addUsageTotals(&state.metrics, turn)
			state.realtimeTurn = usageMetrics{Realtime: true}
			return
		}
		mergeUsageNonZero(&state.realtimeTurn, metric)
		return
	}
	if usageEventIsTerminal(eventType) {
		if usageHasTokens(metric) || !usageHasTokens(state.metrics) {
			state.metrics = metric
		}
		return
	}
	mergeUsageNonZero(&state.metrics, metric)
	if eventType == "message_start" || eventType == "message_delta" {
		state.metrics.TotalTokens = state.metrics.InputTokens + state.metrics.OutputTokens
	}
}

func finalizeRealtimeUsage(state *usageMetricsState) {
	if state == nil || !state.metrics.Realtime {
		return
	}
	addUsageTotals(&state.metrics, state.realtimeTurn)
	state.realtimeTurn = usageMetrics{Realtime: true}
}

func observeUsageValue(state *usageMetricsState, value map[string]any, eventType string) {
	if state == nil {
		return
	}
	if declaredType, ok := value["type"].(string); ok && strings.TrimSpace(declaredType) != "" {
		eventType = declaredType
	}
	metric := extractUsage(value)
	if !metric.Known && state.metrics.Realtime && usageEventIsTerminal(eventType) {
		addUsageTotals(&state.metrics, state.realtimeTurn)
		state.realtimeTurn = usageMetrics{Realtime: true}
		return
	}
	observeUsageMetric(state, metric, eventType)
}

func observeSSEBlock(state *usageMetricsState, block []byte, observers ...func(map[string]any, string)) {
	if state == nil || len(block) == 0 {
		return
	}
	var eventType string
	var data []string
	for _, line := range strings.Split(string(block), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(data) == 0 {
		return
	}
	payload := strings.TrimSpace(strings.Join(data, "\n"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	var value map[string]any
	if json.Unmarshal([]byte(payload), &value) != nil {
		return
	}
	for _, observe := range observers {
		observe(value, eventType)
	}
	observeUsageValue(state, value, eventType)
}

func observeSSEUsage(state *usageMetricsState, chunk []byte, final bool, observers ...func(map[string]any, string)) {
	if state == nil {
		return
	}
	state.streamBuffer = append(state.streamBuffer, chunk...)
	state.streamBuffer = bytes.ReplaceAll(state.streamBuffer, []byte("\r\n"), []byte("\n"))
	const maxStreamBuffer = 1 << 20
	if len(state.streamBuffer) > maxStreamBuffer {
		state.streamBuffer = append([]byte(nil), state.streamBuffer[len(state.streamBuffer)-maxStreamBuffer:]...)
	}
	for {
		separator := bytes.Index(state.streamBuffer, []byte("\n\n"))
		if separator < 0 {
			break
		}
		observeSSEBlock(state, state.streamBuffer[:separator], observers...)
		state.streamBuffer = state.streamBuffer[separator+2:]
	}
	if final && len(state.streamBuffer) > 0 {
		observeSSEBlock(state, state.streamBuffer, observers...)
		state.streamBuffer = nil
	}
}

func numberValue(value any) int64 {
	switch number := value.(type) {
	case json.Number:
		result, _ := number.Int64()
		return result
	case float64:
		return int64(number)
	case int:
		return int64(number)
	case int64:
		return number
	}
	return 0
}
func numberFloat(value any) float64 {
	if number, ok := value.(json.Number); ok {
		result, _ := number.Float64()
		return result
	}
	if number, ok := value.(float64); ok {
		return number
	}
	return 0
}

func usageFromSSE(data []byte) usageMetrics {
	state := &usageMetricsState{}
	observeSSEUsage(state, data, true)
	return state.metrics
}
func (a *app) refresh(ctx context.Context, p provider) (provider, error) {
	if a.database != nil {
		cipher, err := admininfra.NewCredentialCipher(a.encryptionKey)
		if err != nil {
			return provider{}, err
		}
		service := adminapp.NewProviderService(admininfra.NewProviderRepository(a.database), cipher.Encrypt, nil, cipher.Decrypt).WithSubscriptionClient(subscription.Client{HTTP: a.client})
		_, plain, err := service.Credential(ctx, uint64(p.ID))
		if err != nil {
			return provider{}, err
		}
		credentials, err := subscription.Decode(plain, p.Platform)
		if err != nil {
			return provider{}, err
		}
		return withSubscriptionCredential(p, credentials), nil
	}
	if time.Until(p.ExpiresAt) > time.Minute {
		return p, nil
	}
	t, err := a.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {p.RefreshToken}, "client_id": {clientID}, "scope": {"openid profile email"}})
	if err != nil {
		return provider{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.data.Providers {
		if a.data.Providers[i].ID == p.ID {
			a.data.Providers[i].AccessToken = t.AccessToken
			if t.RefreshToken != "" {
				a.data.Providers[i].RefreshToken = t.RefreshToken
			}
			a.data.Providers[i].ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
			if err := a.save(); err != nil {
				return provider{}, err
			}
			return a.data.Providers[i], nil
		}
	}
	return provider{}, errors.New("provider removed")
}
func (a *app) gateway(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-api-key, x-goog-api-key, anthropic-version, anthropic-beta, OpenAI-Beta")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	p, _ := r.Context().Value(gatewayProviderRequestKey{}).(provider)
	if p.Type == "" {
		if prov, ok := a.authenticateKey(r); ok {
			p = prov
		}
	}
	path := gatewayPath(r)
	if r.Method == http.MethodGet {
		if path == "models" {
			a.models(w, r, p)
			return
		}
		if strings.HasPrefix(path, "models/") {
			a.model(w, r, p, strings.TrimPrefix(path, "models/"))
			return
		}
	}
	var err error
	if p.Type == "oauth" {
		p, err = a.refresh(r.Context(), p)
	}
	if err != nil {
		recordGatewayFailure(r, "provider_configuration", errorcode.ProviderUnavailable, "oauth", "provider OAuth token refresh failed")
		fail(w, 503, errorcode.ProviderUnavailable)
		return
	}
	if p.Type == "unconfigured" {
		recordGatewayFailure(r, "provider_configuration", errorcode.ProviderUnavailable, "provider", "no active upstream provider is configured for this key")
		respond(w, 503, map[string]any{
			"error": map[string]string{
				"code":    string(errorcode.ProviderUnavailable),
				"message": "API Key is valid, but no active upstream Provider is configured in BOS Admin (/admin/providers) yet.",
			},
		})
		return
	}
	if strings.HasPrefix(path, "images/") {
		a.imageGateway(w, r, p, path)
		return
	}
	if p.Type == "api_key" || p.Type == "oauth" && (p.Platform == "anthropic" || p.Platform == "gemini" || p.Platform == "antigravity") {
		a.apiKeyGateway(w, r, p, path)
		return
	}
	switch {
	case path == "responses" && r.Method == "POST":
		a.responses(w, r, p)
	case path == "chat/completions" && r.Method == "POST":
		a.codexChat(w, r, p)
	case path == "messages" && r.Method == "POST":
		a.compat(w, r, p, "messages")
	case path == "messages/count_tokens" && r.Method == "POST":
		a.countTokens(w, r, p)
	case path == "responses/compact" && r.Method == "POST":
		a.compact(w, r, p)
	case path == "responses/input_tokens" && r.Method == "POST":
		a.inputTokens(w, r)
	case path == "responses" && r.Method == "GET":
		a.responsesWebSocket(w, r, p)
	case path == "embeddings" && r.Method == "POST":
		recordGatewayFailure(r, "routing", errorcode.ProviderCapabilityUnavailable, "endpoint", "embeddings are not supported by this provider type")
		fail(w, 403, errorcode.ProviderCapabilityUnavailable)
	case path == "live" && r.Method == "POST":
		a.live(w, r, p)
	case strings.HasPrefix(path, "live/") && r.Method == "GET":
		a.liveSideband(w, r, p, strings.TrimPrefix(path, "live/"))
	case path == "alpha/search" && r.Method == "POST":
		a.search(w, r, p)
	case path == "usage" && r.Method == "GET":
		respond(w, 200, map[string]any{"data": map[string]any{"quota": "unlimited"}})
	default:
		recordGatewayFailure(r, "routing", errorcode.NotFound, "endpoint", "method or endpoint is not supported by this provider")
		fail(w, 404, errorcode.NotFound)
	}
}
func (a *app) upstreamRequest(ctx context.Context, p provider, path string, payload []byte) (*http.Response, error) {
	return a.upstreamRequestWithClient(ctx, p, path, payload, a.client)
}
func (a *app) upstreamRequestWithClient(ctx context.Context, p provider, path string, payload []byte, upstreamClient *http.Client) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", upstream+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.AccessToken)
	req.Header.Set("ChatGPT-Account-ID", p.AccountID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	subscription.ApplyCodexClientHeaders(req.Header)
	client := *upstreamClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := a.doUpstream(&client, req, p, payload, a.client.Timeout)
	observeUpstreamResult(ctx, p, res, err)
	return res, err
}
func validResponses(q map[string]json.RawMessage) bool {
	var model string
	if json.Unmarshal(q["model"], &model) != nil || len(model) == 0 || len(model) > 100 {
		return false
	}
	if len(q["input"]) == 0 || bytes.Equal(q["input"], []byte("null")) {
		return false
	}
	return true
}
func (a *app) responses(w http.ResponseWriter, r *http.Request, p provider) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestBodyLimit(r)))
	if err != nil {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	raw = rewriteModelInJSON(raw, p.ModelMapping)
	var q map[string]json.RawMessage
	if json.Unmarshal(raw, &q) != nil || !validResponses(q) {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	var clientStream bool
	if len(q["stream"]) > 0 && json.Unmarshal(q["stream"], &clientStream) != nil {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	q["stream"] = json.RawMessage("true")
	q["store"] = json.RawMessage("false")
	if len(q["instructions"]) == 0 {
		q["instructions"] = json.RawMessage(`""`)
	}
	var inputText string
	if json.Unmarshal(q["input"], &inputText) == nil {
		normalized, _ := json.Marshal([]any{map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": inputText}}}})
		q["input"] = normalized
	}
	raw, err = json.Marshal(q)
	if err != nil {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	res, err := a.upstreamRequest(r.Context(), p, "/responses", raw)
	if err != nil {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		logging.Printf(r.Context(), "WARN", "gateway.upstream.bad_status", "status=%d", res.StatusCode)
		if res.StatusCode == http.StatusTooManyRequests {
			writeUpstreamLimitError(w, res, time.Now())
			return
		}
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	if !clientStream {
		completed, err := completedResponse(res.Body)
		if err != nil {
			fail(w, 502, errorcode.UpstreamError)
			return
		}
		completedBytes, _ := json.Marshal(completed)
		r = withUsageMetrics(r, extractUsage(completed))
		a.writeToolJSON(w, r, http.StatusOK, completedBytes)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	a.forwardInspectedBody(w, r, res.Body, http.StatusOK, "text/event-stream")
}

func appendSecurityAuditPayload(current, chunk []byte) []byte {
	const maxSecurityAuditBody = 1 << 20
	if len(current) >= maxSecurityAuditBody {
		return current
	}
	remaining := maxSecurityAuditBody - len(current)
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
	}
	return append(current, chunk...)
}

type securityAuditScanner struct {
	app     *app
	request *http.Request
	window  []byte
	seen    bool
}

func (scanner *securityAuditScanner) Write(chunk []byte) {
	if securityInspectionDisabled(scanner.request, domain.ToolDirectionResponse) {
		return
	}
	if scanner.seen || len(scanner.window) >= 1<<20 {
		return
	}
	if remaining := (1 << 20) - len(scanner.window); len(chunk) > remaining {
		chunk = chunk[:remaining]
	}
	scanner.window = append(scanner.window, chunk...)
	if scanner.app == nil || scanner.request == nil {
		return
	}
	if inspector, ok := scanner.app.background.SecurityEvents.(SecurityContentInspector); ok {
		result, err := inspector.InspectContent(scanner.request.Context(), "model_output_stream", scanner.window)
		if err == nil && result.RuleID != "" {
			scanner.app.inspectModelContent(scanner.request, "model_output_stream", scanner.window)
			scanner.seen = true
			scanner.window = nil
			return
		}
	}
	if len(scanner.window) > 256 {
		scanner.window = append(scanner.window[:0], scanner.window[len(scanner.window)-256:]...)
	}
}

// models reads the published catalog for the authenticated tenant, never a global fallback.
func (a *app) models(w http.ResponseWriter, r *http.Request, p provider) {
	if geminiNativePath(r) == "/v1beta/models" {
		a.geminiModels(w, r, p, "")
		return
	}

	if a.database == nil {
		a.configuredModels(w, r, p)
		return
	}
	key, ok := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	if !ok {
		key, ok = a.authenticatedKey(r)
	}
	if !ok || key.OrgID <= 0 {
		fail(w, http.StatusUnauthorized, errorcode.Unauthorized)
		return
	}
	models, err := gatewayinfra.NewModelCatalogRepository(a.database).ListAvailableModelsForKey(r.Context(), uint64(key.OrgID), key.AllowedProviderIDs, key.AllowedModels)
	if err != nil {
		logging.Printf(r.Context(), "ERROR", "gateway.models.lookup_failed", "organization_id=%d err=%v", key.OrgID, err)
		fail(w, http.StatusInternalServerError, errorcode.InternalError)
		return
	}
	items := make([]map[string]any, 0, len(models))
	seen := make(map[string]bool)
	for _, model := range models {
		model.ID = upstreamurl.ModelID(model.APIFormat, model.ID)
		if seen[model.ID] || model.ID == "" || model.ID == "*" {
			continue
		}
		seen[model.ID] = true
		items = append(items, map[string]any{
			"id": model.ID, "slug": model.ID, "name": model.ID,
			"object": "model", "created": 1735689600, "owned_by": model.Platform,
		})
	}
	logging.Printf(r.Context(), "INFO", "gateway.models.response", "format=openai data_field=true models_field=true raw_catalog_count=%d models_count=%d", len(models), len(items))
	respond(w, http.StatusOK, map[string]any{"object": "list", "data": items, "models": items})
}
func (a *app) model(w http.ResponseWriter, r *http.Request, p provider, id string) {
	if strings.HasPrefix(geminiNativePath(r), "/v1beta/models/") {
		a.geminiModels(w, r, p, id)
		return
	}

	if id == "" {
		fail(w, 404, errorcode.NotFound)
		return
	}
	rec := &recordingWriter{header: make(http.Header)}
	a.models(rec, r, p)
	if rec.status != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.status)
		_, _ = w.Write(rec.buffer.Bytes())
		return
	}
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(rec.buffer.Bytes(), &list) != nil {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	for _, m := range list.Data {
		if m["id"] == id {
			respond(w, 200, m)
			return
		}
	}
	fail(w, 404, errorcode.NotFound)
}

type recordingWriter struct {
	header http.Header
	status int
	buffer bytes.Buffer
}

func (w *recordingWriter) Header() http.Header    { return w.header }
func (w *recordingWriter) WriteHeader(status int) { w.status = status }
func (w *recordingWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.buffer.Write(b)
}
func (a *app) compat(w http.ResponseWriter, r *http.Request, p provider, kind string) {
	var q struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
		System    any  `json:"system"`
		Stream    bool `json:"stream"`
		MaxTokens int  `json:"max_tokens"`
	}
	if body(r, &q) != nil || q.Model == "" || len(q.Model) > 100 || len(q.Messages) == 0 || q.Stream {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	input := make([]map[string]any, 0, len(q.Messages)+1)
	if q.System != nil {
		input = append(input, map[string]any{"role": "developer", "content": q.System})
	}
	for _, m := range q.Messages {
		if m.Role != "user" && m.Role != "assistant" && m.Role != "system" && m.Role != "developer" {
			fail(w, 400, errorcode.InvalidRequest)
			return
		}
		input = append(input, map[string]any{"role": m.Role, "content": m.Content})
	}
	payload, _ := json.Marshal(map[string]any{"model": q.Model, "input": input, "instructions": "", "stream": true, "store": false})
	res, err := a.upstreamRequest(r.Context(), p, "/responses", payload)
	if err != nil {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		logging.Printf(r.Context(), "WARN", "gateway.upstream.bad_status", "status=%d", res.StatusCode)
		if res.StatusCode == http.StatusTooManyRequests {
			writeUpstreamLimitError(w, res, time.Now())
			return
		}
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	response, err := completedResponse(res.Body)
	if err != nil {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	content := extractText(response)
	responseBytes, _ := json.Marshal(response)
	a.inspectResponseToolCalls(r, responseBytes)
	a.inspectModelContent(r, "model_output", []byte(content))
	if kind == "messages" {
		respond(w, 200, map[string]any{"id": response["id"], "type": "message", "role": "assistant", "model": q.Model, "content": []any{map[string]string{"type": "text", "text": content}}, "stop_reason": "end_turn"})
		return
	}
	respond(w, 200, map[string]any{"id": response["id"], "object": "chat.completion", "model": q.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
}
func extractText(response map[string]any) string {
	items, _ := response["output"].([]any)
	var out strings.Builder
	for _, item := range items {
		m, _ := item.(map[string]any)
		parts, _ := m["content"].([]any)
		for _, part := range parts {
			p, _ := part.(map[string]any)
			if p["type"] == "output_text" {
				if s, ok := p["text"].(string); ok {
					out.WriteString(s)
				}
			}
		}
	}
	return out.String()
}

func completedResponse(reader io.Reader) (map[string]any, error) {
	all, err := io.ReadAll(io.LimitReader(reader, 8<<20))
	if err != nil {
		return nil, err
	}
	items := map[int]any{}
	var completed map[string]any
	var textParts strings.Builder
	for _, line := range bytes.Split(all, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		var event struct {
			Type        string         `json:"type"`
			OutputIndex int            `json:"output_index"`
			Item        any            `json:"item"`
			Delta       string         `json:"delta"`
			Response    map[string]any `json:"response"`
		}
		if json.Unmarshal(bytes.TrimPrefix(line, []byte("data: ")), &event) != nil {
			continue
		}
		switch event.Type {
		case "response.output_item.done":
			if event.Item != nil {
				items[event.OutputIndex] = event.Item
			}
		case "response.output_text.delta":
			textParts.WriteString(event.Delta)
		case "response.completed":
			completed = event.Response
		}
	}
	if completed == nil {
		return nil, errors.New("completion event missing")
	}
	if output, ok := completed["output"].([]any); ok && len(output) > 0 {
		return completed, nil
	}
	indexes := make([]int, 0, len(items))
	for index := range items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	output := make([]any, 0, len(indexes))
	for _, index := range indexes {
		output = append(output, items[index])
	}
	if len(output) == 0 && textParts.Len() > 0 {
		output = append(output, map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": textParts.String()}}})
	}
	completed["output"] = output
	return completed, nil
}

func nextProviderID(items []provider) int {
	id := 0
	for _, p := range items {
		if p.ID > id {
			id = p.ID
		}
	}
	return id + 1
}
func nextKeyID(items []apiKey) int {
	id := 0
	for _, k := range items {
		if k.ID > id {
			id = k.ID
		}
	}
	return id + 1
}
