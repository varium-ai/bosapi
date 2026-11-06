package interfaces

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"bosapi/backend/internal/analytics/application"
	"bosapi/backend/internal/analytics/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

type Handler struct{ service *application.Service }

func NewHandler(service *application.Service) *Handler { return &Handler{service: service} }

// RegisterRoutes must be mounted beneath the existing super_admin middleware.
func (h *Handler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/usage-reporting", h.get)
	group.PUT("/usage-reporting", h.put)
	group.POST("/usage-reporting/preview", h.preview)
	group.POST("/usage-reporting/reset-installation", h.reset)
}

func (h *Handler) get(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	httpapi.WriteSuccess(c, http.StatusOK, h.service.Status(c.Request.Context()))
}

func (h *Handler) put(c *gin.Context) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	d := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || body.Enabled == nil || d.Decode(new(any)) != io.EOF {
		httpapi.WriteError(c, http.StatusBadRequest, errorcode.InvalidRequest, "enabled must be a boolean and the only field")
		return
	}
	if err := h.service.Toggle(c.Request.Context(), *body.Enabled, actor(c)); err != nil {
		writeError(c, err)
		return
	}
	h.get(c)
}

func (h *Handler) preview(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	preview, err := h.service.Preview(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	httpapi.WriteSuccess(c, http.StatusOK, preview)
}

func (h *Handler) reset(c *gin.Context) {
	if err := h.service.Reset(c.Request.Context(), actor(c)); err != nil {
		writeError(c, err)
		return
	}
	h.get(c)
}

func actor(c *gin.Context) *uint64 {
	if claims, ok := httpapi.CurrentClaims(c); ok {
		id := claims.UserID
		return &id
	}
	return nil
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrStale):
		httpapi.WriteError(c, http.StatusConflict, errorcode.InvalidRequest, "usage reporting configuration conflicts with this operation")
	case errors.Is(err, domain.ErrRateLimited):
		c.Header("Retry-After", "10")
		httpapi.WriteError(c, http.StatusTooManyRequests, errorcode.InvalidRequest, "preview is limited to once every 10 seconds per replica")
	default:
		httpapi.WriteError(c, http.StatusServiceUnavailable, errorcode.InternalError, "usage reporting storage or configuration is unavailable")
	}
}
