package interfaces

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	analyticsdomain "bosapi/backend/internal/analytics/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

type smtpConnectionTester interface {
	TestSMTP(context.Context) (bool, error)
}

// SystemSettingHandler handles public platform configs and administrative system settings.
type SystemSettingHandler struct {
	service    *application.SystemSettingService
	smtpTester smtpConnectionTester
}

// NewSystemSettingHandler constructs a new SystemSettingHandler.
func NewSystemSettingHandler(service *application.SystemSettingService, testers ...smtpConnectionTester) *SystemSettingHandler {
	handler := &SystemSettingHandler{service: service}
	if len(testers) > 0 {
		handler.smtpTester = testers[0]
	}
	return handler
}

// RegisterPublicRoutes registers unauthenticated or tenant platform configuration endpoints.
func (handler *SystemSettingHandler) RegisterPublicRoutes(group *gin.RouterGroup) {
	group.GET("/platform/config", handler.getPublicConfig)
}

// RegisterAdminRoutes registers administrative setting management endpoints.
func (handler *SystemSettingHandler) RegisterAdminRoutes(group *gin.RouterGroup) {
	group.GET("/tool-security-policy", handler.getToolSecurityPolicy)
	group.PUT("/tool-security-policy", handler.updateToolSecurityPolicy)
	group.GET("/settings/gateway-endpoints", handler.getGatewayEndpoints)
	group.PUT("/settings/gateway-endpoints", handler.updateGatewayEndpoints)
	group.DELETE("/settings/gateway-endpoints", handler.resetGatewayEndpoints)

	group.GET("/settings", handler.listSettings)
	group.GET("/settings/:key", handler.getSetting)
	group.PUT("/settings/:key", handler.setSetting)
	group.DELETE("/settings/:key", handler.deleteSetting)
	group.GET("/message-notification-settings", handler.getMessageNotificationSettings)
	group.PUT("/message-notification-settings", handler.updateMessageNotificationSettings)
	group.POST("/message-notification-settings/test", handler.testSMTPConnection)
}

func (handler *SystemSettingHandler) getMessageNotificationSettings(ctx *gin.Context) {
	config, err := handler.service.GetMessageNotificationConfig(ctx.Request.Context())
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "failed to read message notification settings")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{
		"provider": config.Provider, "smtp_host": config.SMTPHost, "smtp_port": config.SMTPPort,
		"smtp_username": config.SMTPUsername, "from_email": config.FromEmail, "from_name": config.FromName,
		"alert_emails": config.AlertEmails, "has_lark_webhook": config.LarkWebhook != "",
		"masked_lark_webhook": maskLarkWebhook(config.LarkWebhook),
		"has_smtp_password":   config.SMTPPassword != "",
		"has_webhook_url":     config.WebhookURL != "",
		"masked_webhook_url":  maskMessageWebhookURL(config.WebhookURL),
	})
}

func maskMessageWebhookURL(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "****"
	}
	return "https://" + parsed.Host + "/****"
}

func maskLarkWebhook(webhook string) string {
	trimmed := strings.TrimSpace(webhook)
	if trimmed == "" {
		return ""
	}
	const hookPrefix = "/open-apis/bot/v2/hook/"
	idx := strings.Index(trimmed, hookPrefix)
	if idx == -1 {
		if len(trimmed) > 20 {
			return trimmed[:12] + "****" + trimmed[len(trimmed)-4:]
		}
		return "****"
	}
	base := trimmed[:idx+len(hookPrefix)]
	token := trimmed[idx+len(hookPrefix):]
	if len(token) <= 4 {
		return base + "****"
	} else if len(token) <= 8 {
		return base + token[:1] + "****" + token[len(token)-1:]
	} else {
		return base + token[:4] + "****" + token[len(token)-4:]
	}
}

func (handler *SystemSettingHandler) updateMessageNotificationSettings(ctx *gin.Context) {
	var request struct {
		Provider     string   `json:"provider" binding:"required,oneof=lark email webhook"`
		LarkWebhook  string   `json:"lark_webhook"`
		WebhookURL   string   `json:"webhook_url"`
		SMTPHost     string   `json:"smtp_host"`
		SMTPPort     int      `json:"smtp_port"`
		SMTPUsername string   `json:"smtp_username"`
		SMTPPassword string   `json:"smtp_password"`
		FromEmail    string   `json:"from_email"`
		FromName     string   `json:"from_name"`
		AlertEmails  []string `json:"alert_emails"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request body is invalid")
		return
	}
	var updatedBy *uint64
	if claims, ok := httpapi.CurrentClaims(ctx); ok && claims.UserID > 0 {
		updatedBy = &claims.UserID
	}
	err := handler.service.UpdateMessageNotificationConfig(ctx.Request.Context(), domain.MessageNotificationConfig{
		Provider: request.Provider, LarkWebhook: request.LarkWebhook, WebhookURL: request.WebhookURL, SMTPHost: request.SMTPHost, SMTPPort: request.SMTPPort,
		SMTPUsername: request.SMTPUsername, SMTPPassword: request.SMTPPassword, FromEmail: request.FromEmail,
		FromName: request.FromName, AlertEmails: request.AlertEmails,
	}, updatedBy)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{"status": "saved"})
}

func (handler *SystemSettingHandler) getPublicConfig(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	config, err := handler.service.GetPublicPlatformConfig(ctx.Request.Context())
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "failed to read platform configuration")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, config)
}

func (handler *SystemSettingHandler) getGatewayEndpoints(ctx *gin.Context) {
	endpoints, err := handler.service.GetGatewayEndpoint(ctx.Request.Context())
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "failed to read gateway endpoints")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, endpoints)
}

type updateGatewayEndpointsRequest struct {
	Endpoint string `json:"endpoint" binding:"required"`
}

func (handler *SystemSettingHandler) updateGatewayEndpoints(ctx *gin.Context) {
	var req updateGatewayEndpointsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid request body")
		return
	}
	var updatedBy *uint64
	if claims, ok := httpapi.CurrentClaims(ctx); ok && claims.UserID > 0 {
		updatedBy = &claims.UserID
	}
	endpoints, err := handler.service.UpdateGatewayEndpoint(ctx.Request.Context(), domain.GatewayEndpoint{
		Endpoint: req.Endpoint,
	}, updatedBy)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, endpoints)
}

func (handler *SystemSettingHandler) resetGatewayEndpoints(ctx *gin.Context) {
	endpoints, err := handler.service.ClearGatewayEndpoint(ctx.Request.Context())
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "failed to reset gateway endpoints")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, endpoints)
}

func (handler *SystemSettingHandler) listSettings(ctx *gin.Context) {
	prefix := ctx.Query("prefix")
	if prefix == "" || strings.HasPrefix(application.SettingKeyMessageNotifications, prefix) || strings.HasPrefix(prefix, "notifications") {
		prefix = "__not_a_setting_prefix__"
	}
	settings, err := handler.service.ListSettings(ctx.Request.Context(), prefix)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "failed to list settings")
		return
	}
	// Mask secret settings
	for i := range settings {
		if settings[i].IsSecret {
			settings[i].ValueText = "******"
		}
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": settings})
}

func (handler *SystemSettingHandler) getSetting(ctx *gin.Context) {
	key := ctx.Param("key")
	if key == application.SettingKeyMessageNotifications {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "setting was not found")
		return
	}
	setting, err := handler.service.GetSetting(ctx.Request.Context(), key)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "failed to read setting")
		return
	}
	if setting == nil {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "setting was not found")
		return
	}
	if setting.IsSecret {
		setting.ValueText = "******"
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, setting)
}

type setSettingRequest struct {
	ValueText string `json:"value_text"`
	IsSecret  bool   `json:"is_secret"`
}

func (handler *SystemSettingHandler) setSetting(ctx *gin.Context) {
	key := ctx.Param("key")
	if analyticsdomain.ReservedSetting(key) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "use the usage-reporting endpoint")
		return
	}
	if key == domain.SettingKeyToolSecurityMode {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "use the tool-security-policy endpoint")
		return
	}
	if key == application.SettingKeyMessageNotifications {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "setting was not found")
		return
	}
	var req setSettingRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid request body")
		return
	}
	var updatedBy *uint64
	if claims, ok := httpapi.CurrentClaims(ctx); ok && claims.UserID > 0 {
		updatedBy = &claims.UserID
	}
	if err := handler.service.SetSetting(ctx.Request.Context(), key, req.ValueText, req.IsSecret, updatedBy); err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "failed to save setting")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"key": key, "status": "saved"})
}

func (handler *SystemSettingHandler) deleteSetting(ctx *gin.Context) {
	key := ctx.Param("key")
	if analyticsdomain.ReservedSetting(key) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "use the usage-reporting endpoint")
		return
	}
	if key == domain.SettingKeyToolSecurityMode {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "use the tool-security-policy endpoint")
		return
	}
	if key == application.SettingKeyMessageNotifications {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "setting was not found")
		return
	}
	if err := handler.service.DeleteSetting(ctx.Request.Context(), key); err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "failed to delete setting")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"key": key, "status": "deleted"})
}

func (handler *SystemSettingHandler) testSMTPConnection(ctx *gin.Context) {
	if handler.smtpTester == nil {
		httpapi.WriteError(ctx, http.StatusServiceUnavailable, errorcode.InternalError, "SMTP connection testing is unavailable")
		return
	}
	bounded, cancel := context.WithTimeout(ctx.Request.Context(), 12*time.Second)
	defer cancel()
	encrypted, err := handler.smtpTester.TestSMTP(bounded)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusBadGateway, errorcode.InternalError, "SMTP connection test failed; check saved settings")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{"connected": true, "encrypted": encrypted})
}
