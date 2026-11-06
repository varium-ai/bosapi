package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	analyticsapp "bosapi/backend/internal/analytics/application"
	analyticsinterfaces "bosapi/backend/internal/analytics/interfaces"
	gatewayapp "bosapi/backend/internal/gateway/application"

	adminapp "bosapi/backend/internal/admin/application"
	admininfra "bosapi/backend/internal/admin/infrastructure"
	admininterfaces "bosapi/backend/internal/admin/interfaces"
	authapp "bosapi/backend/internal/auth/application"
	authDomain "bosapi/backend/internal/auth/domain"
	authinfra "bosapi/backend/internal/auth/infrastructure"
	authinterfaces "bosapi/backend/internal/auth/interfaces"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/geoip"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/usagerollup"
	"bosapi/backend/internal/platform/usagestore"
	userapp "bosapi/backend/internal/user/application"
	userinfra "bosapi/backend/internal/user/infrastructure"
	userinterfaces "bosapi/backend/internal/user/interfaces"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// PortalRouter composes auth, admin, and user modules over the shared database.
type PortalRouter struct {
	AuthService *authapp.Service
	Handler     http.Handler
	Close       func()
}

// NewPortalRouter wires domain modules without exposing Gorm to HTTP handlers.
func NewPortalRouter(database *gorm.DB, jwtSecret []byte, accessTTL, refreshTTL time.Duration, maxBodyBytes int64, credentialKey []byte, fallback http.Handler, loginLockStore authDomain.LoginLockStore, runtime ...admininterfaces.RuntimeStatus) (*PortalRouter, error) {
	geoPath := os.Getenv("BOS_GEOIP_DB")
	if geoPath == "" {
		candidates := []string{
			filepath.Join("/app", "resources", "GeoLite2-City.mmdb.gz"),
			filepath.Join("resources", "GeoLite2-City.mmdb.gz"),
			filepath.Join("data", "GeoLite2-City.mmdb.gz"),
			filepath.Join("backend", "data", "GeoLite2-City.mmdb.gz"),
		}
		for _, candidate := range candidates {
			if _, statErr := os.Stat(candidate); statErr == nil {
				geoPath = candidate
				break
			}
		}
	}
	geoResolver, err := geoip.New(geoPath)
	if err != nil {
		logging.Printf(context.Background(), "ERROR", "geoip.resolver.start_failed", "err=%v", err)
	}
	systemSettingRepo := admininfra.NewSystemSettingRepository(database)
	notifier := admininfra.NewConfiguredMessageNotifier(systemSettingRepo)
	authRepository := authinfra.NewRepository(database)
	var authNotifiers []authapp.OTPNotifier
	if notifier != nil {
		authNotifiers = append(authNotifiers, notifier)
	}
	authService := authapp.NewService(authRepository, jwtSecret, accessTTL, refreshTTL, authNotifiers...)
	authService.WithEnforceAdminMFA(os.Getenv("BOS_ENFORCE_ADMIN_MFA") != "false")
	authService.WithLoginLock(loginLockStore)
	var authCipher *authinfra.Cipher
	if len(credentialKey) == 32 {
		if c, err := authinfra.NewCipher(credentialKey); err == nil {
			authCipher = c
			authService.WithCipher(authCipher)
		}
	}
	authHandler := authinterfaces.NewHandler(authService)
	keyRepository := userinfra.NewKeyRepository(database)
	keyService := userapp.NewKeyService(keyRepository)
	var apiKeyCipher *admininfra.APIKeyCipher
	if len(credentialKey) == 32 {
		if c, err := admininfra.NewAPIKeyCipher(credentialKey); err == nil {
			apiKeyCipher = c
			keyService.WithKeyCipher(apiKeyCipher)
		}
	}
	router := gin.New()
	statusHandler := admininterfaces.NewStatusHandler(database, time.Now(), runtime...)
	monitorReady := false
	defer func() {
		if !monitorReady {
			statusHandler.Monitor.Close()
		}
	}()
	router.Use(statusHandler.Monitor.Middleware())
	router.Use(gin.Recovery(), httpapi.Middleware(maxBodyBytes), portalAccessLogMiddleware())
	router.GET("/health/live", func(ctx *gin.Context) {
		httpapi.WriteSuccess(ctx, http.StatusOK, struct {
			Status string `json:"status"`
		}{"ok"})
	})
	router.GET("/health/ready", func(ctx *gin.Context) {
		sqlDatabase, err := database.DB()
		if err != nil || sqlDatabase.PingContext(ctx.Request.Context()) != nil {
			httpapi.WriteError(ctx, http.StatusServiceUnavailable, errorcode.ProviderUnavailable, "database is unavailable")
			return
		}
		httpapi.WriteSuccess(ctx, http.StatusOK, struct {
			Status string `json:"status"`
		}{"ok"})
	})
	api := router.Group("/api/v1")
	auth := api.Group("/auth")
	authHandler.RegisterPublicRoutes(auth)
	keyPortal := admininterfaces.NewKeyPortalHandler(
		keyRepository,
		jwtSecret,
		adminapp.NewAdminUsageService(admininfra.NewAdminUsageRepository(database, geoResolver)),
		adminapp.NewAdminErrorService(admininfra.NewAdminErrorRepository(database)),
		adminapp.NewSecurityEventService(admininfra.NewSecurityEventRepository(database, geoResolver)),
	)
	keyPortal.RegisterPublicRoutes(auth)
	keyPortalGroup := api.Group("/key-portal")
	keyPortal.RegisterRoutes(keyPortalGroup)
	announcementHandler := admininterfaces.NewAnnouncementHandler(adminapp.NewAnnouncementService(admininfra.NewAnnouncementRepository(database)))
	announcementHandler.RegisterReadRoutes(keyPortalGroup)
	publicModelHandler := admininterfaces.NewPublicModelHandler(adminapp.NewPublicModelService(admininfra.NewPublicModelRepository(database)))
	systemSettingService := adminapp.NewSystemSettingService(systemSettingRepo)
	systemSettingHandler := admininterfaces.NewSystemSettingHandler(systemSettingService, notifier)
	systemSettingHandler.RegisterPublicRoutes(api)
	protected := api.Group("")
	protected.Use(authHandler.Authenticate())
	publicModelHandler.RegisterRoutes(protected)
	authHandler.RegisterProtectedRoutes(protected.Group("/auth"))
	userinterfaces.NewKeyHandler(keyService, keyRepository).RegisterRoutes(protected)
	userinterfaces.NewUsageHandler(userapp.NewUsageService(keyRepository)).RegisterRoutes(protected)
	admin := protected.Group("/admin")
	admin.Use(authinterfaces.RequireRoles("org_admin", "super_admin"))
	announcementHandler.RegisterAdminRoutes(admin)
	requestLogRepository := admininfra.NewRequestLogRepository(database, geoResolver)
	defer func() {
		if !monitorReady {
			requestLogRepository.Close()
		}
	}()
	logging.SetSink(requestLogRepository)
	admininterfaces.NewRequestLogHandler(adminapp.NewRequestLogService(requestLogRepository)).RegisterRoutes(admin)
	organizationRepository := admininfra.NewOrganizationRepository(database)
	admininterfaces.NewOrganizationHandler(adminapp.NewOrganizationService(organizationRepository)).RegisterRoutes(admin)
	admininterfaces.NewSetupHandler(database).RegisterRoutes(admin.Group("", authinterfaces.RequireRoles("org_admin", "super_admin")))
	var snapshotRedis redis.UniversalClient
	if len(runtime) > 0 {
		snapshotRedis = runtime[0].SnapshotRedis
	}
	var closeSnapshotRedis = func() {}
	if client, ok := snapshotRedis.(*redis.Client); ok {
		options := *client.Options()
		options.ContextTimeoutEnabled = true
		options.MaxRetries = -1
		options.PoolSize = 2
		options.MinIdleConns = 0
		options.DialTimeout, options.ReadTimeout, options.WriteTimeout, options.PoolTimeout = 150*time.Millisecond, 150*time.Millisecond, 150*time.Millisecond, 150*time.Millisecond
		owned := redis.NewClient(&options)
		snapshotRedis = owned
		closeSnapshotRedis = func() { _ = owned.Close() }
	}
	defer func() {
		if !monitorReady {
			closeSnapshotRedis()
		}
	}()
	adminKeyRepository := admininfra.NewCachedAdminKeyRepository(database, snapshotRedis)
	defer func() {
		if !monitorReady {
			adminKeyRepository.Close()
		}
	}()
	adminKeyService := adminapp.NewAdminKeyService(adminKeyRepository)
	if apiKeyCipher != nil {
		adminKeyService.WithKeyCipher(apiKeyCipher)
	}
	if authCipher != nil {
		adminKeyService.WithAuthCipher(authCipher)
	}
	admininterfaces.NewAdminKeyHandler(adminKeyService).RegisterRoutes(admin)
	admininterfaces.NewKeyTemplateHandler(adminapp.NewKeyTemplateService(admininfra.NewKeyTemplateRepository(database))).RegisterRoutes(admin)
	usageRepository := admininfra.NewSharedAdminUsageRepository(database, snapshotRedis, geoResolver)
	portalUsageRepository := admininfra.NewSharedPortalUsageRepository(database, snapshotRedis, geoResolver)
	keyPortal.WithUsageService(adminapp.NewAdminUsageService(portalUsageRepository))
	keyPortal.WithIPUsageService(adminapp.NewAdminUsageService(usageRepository))
	defer func() {
		if !monitorReady {
			usageRepository.Close()
			portalUsageRepository.Close()
		}
	}()
	admininterfaces.NewAdminUsageHandler(adminapp.NewAdminUsageService(usageRepository)).RegisterRoutes(admin)
	var rateLimitRuntime func(context.Context) (bool, string)
	if len(runtime) > 0 {
		rateLimitRuntime = runtime[0].RateLimit
	}
	admininterfaces.NewRateLimitHandler(adminapp.NewRateLimitService(admininfra.NewRateLimitRepository(database)), rateLimitRuntime).RegisterRoutes(admin)
	statusHandler.RegisterRoutes(admin.Group("", authinterfaces.RequireRoles("super_admin")))
	admininterfaces.NewSecurityEventHandler(adminapp.NewSecurityEventService(admininfra.NewCachedSecurityEventRepository(database, geoResolver))).RegisterRoutes(admin)
	adminUserService := adminapp.NewAdminUserService(admininfra.NewAdminUserRepository(database), authapp.HashPassword)
	if loginLockStore != nil {
		adminUserService.WithLoginLockUnlocker(loginLockStore)
		adminUserService.WithLoginLockStore(loginLockStore)
	}
	admininterfaces.NewAdminUserHandler(adminUserService).RegisterRoutes(admin.Group("", authinterfaces.RequireRoles("org_admin", "super_admin")))
	admininterfaces.NewAdminErrorHandler(adminapp.NewAdminErrorService(admininfra.NewAdminErrorRepository(database))).RegisterRoutes(admin)
	superAdminService := adminapp.NewSuperAdminService(admininfra.NewSuperAdminRepository(database), notifier, authapp.HashPassword, jwtSecret)
	admininterfaces.NewSuperAdminHandler(superAdminService).RegisterRoutes(admin.Group("", authinterfaces.RequireRoles("super_admin")))
	credentialCipher, err := admininfra.NewCredentialCipher(credentialKey)
	if err != nil {
		return nil, err
	}
	modelMetadataRepo := admininfra.NewModelMetadataRepository(database)
	modelMetadataService := adminapp.NewModelMetadataService(modelMetadataRepo)
	admininterfaces.NewModelMetadataHandler(modelMetadataService).RegisterRoutes(admin.Group("", authinterfaces.RequireRoles("super_admin")))

	providerModelService := adminapp.NewProviderModelService(admininfra.NewProviderModelRepository(database))
	providerService := adminapp.NewProviderService(admininfra.NewProviderRepository(database), credentialCipher.Encrypt, admininfra.NewProviderModelRepository(database), credentialCipher.Decrypt, modelMetadataRepo)
	admininterfaces.NewProviderHandler(providerService, providerModelService).RegisterRoutes(admin.Group("", authinterfaces.RequireRoles("org_admin", "super_admin")))
	admininterfaces.NewProviderBindingHandler(adminapp.NewProviderBindingService(admininfra.NewProviderBindingRepository(database))).RegisterRoutes(admin.Group("", authinterfaces.RequireRoles("super_admin")))
	systemSettingHandler.RegisterAdminRoutes(admin.Group("", authinterfaces.RequireRoles("super_admin")))
	var usageReporting *analyticsapp.Service
	if len(runtime) > 0 {
		usageReporting = runtime[0].UsageReporting
	}
	if usageReporting == nil {
		usageReporting = analyticsapp.New(nil, gatewayapp.UsageReportingConfigFromEnv(), nil, "unknown")
	}
	analyticsinterfaces.NewHandler(usageReporting).RegisterRoutes(admin.Group("", authinterfaces.RequireRoles("super_admin")))
	if fallback != nil {
		forward := func(ctx *gin.Context) {
			ctx.Status(http.StatusOK)
			fallback.ServeHTTP(ctx.Writer, ctx.Request)
		}
		router.Any("/v1beta/models", forward)
		router.Any("/v1beta/models/*modelAction", forward)
		router.Any("/v1", forward)
		router.Any("/v1/*path", forward)
		router.Any("/models", forward)
		router.Any("/models/*path", forward)
		router.Any("/responses", forward)
		router.Any("/responses/*path", forward)
		router.Any("/chat/completions", forward)
		router.Any("/messages", forward)
		router.Any("/messages/*path", forward)
		router.Any("/embeddings", forward)
		router.Any("/images/*path", forward)
		router.NoRoute(forward)
	}
	monitorReady = true
	warmCtx, stopWarm := context.WithCancel(context.Background())
	warmDone := make(chan struct{})
	go func() {
		defer close(warmDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			usageRepository.WarmDefault(warmCtx)
			select {
			case <-warmCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	stopRollups := usagerollup.Start(database)
	stopUsageLogs := usagestore.Start(database)
	stopRequestLogBackfill := admininfra.StartRequestLogBackfill(database)
	return &PortalRouter{AuthService: authService, Handler: router, Close: func() {
		stopWarm()
		<-warmDone
		usageRepository.Close()
		portalUsageRepository.Close()
		adminKeyRepository.Close()
		closeSnapshotRedis()
		stopRequestLogBackfill()
		stopUsageLogs()
		stopRollups()
		statusHandler.Monitor.Close()
		requestLogRepository.Close()
	}}, nil
}

func portalAccessLogMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		started := time.Now()
		ctx.Next()
		if !strings.HasPrefix(ctx.Request.URL.Path, "/api/v1/") && ctx.Request.URL.Path != "/api/v1" && !strings.HasPrefix(ctx.Request.URL.Path, "/health/") {
			return
		}
		logging.Printf(ctx.Request.Context(), "INFO", "portal.request.completed", "method=%s path=%s status=%d duration_ms=%d", ctx.Request.Method, ctx.FullPath(), ctx.Writer.Status(), time.Since(started).Milliseconds())
	}
}

// NewHTTPServer creates an HTTP server with explicit network timeouts.
func NewHTTPServer(address string, handler http.Handler, timeout ...time.Duration) *http.Server {
	writeTimeout := gatewayapp.DefaultGatewayTimeout
	if len(timeout) > 0 && timeout[0] > 0 {
		writeTimeout = timeout[0]
	}
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: writeTimeout, IdleTimeout: 120 * time.Second}
}
