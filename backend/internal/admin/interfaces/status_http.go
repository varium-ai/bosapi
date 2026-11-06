package interfaces

import (
	"context"
	"net/http"
	"time"

	analyticsapp "bosapi/backend/internal/analytics/application"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type RuntimeStatus struct {
	SnapshotRedis  redis.UniversalClient
	UsageReporting *analyticsapp.Service
	Redis          func(context.Context) error
	RedisInfo      func(context.Context) (string, error)
	Queue          func() string
	QueueDetails   func() telemetry.Fields
	RateLimit      func(context.Context) (bool, string)
}

type StatusHandler struct{ Monitor *telemetry.Monitor }

func NewStatusHandler(database *gorm.DB, _ time.Time, runtime ...RuntimeStatus) *StatusHandler {
	var state RuntimeStatus
	if len(runtime) > 0 {
		state = runtime[0]
	}
	sources := telemetry.Sources{Redis: state.Redis, RedisInfo: state.RedisInfo, Queue: state.QueueDetails}
	if database != nil {
		sources.Database, _ = database.DB()
	}
	if sources.Queue == nil && state.Queue != nil {
		sources.Queue = func() telemetry.Fields { return telemetry.Fields{"state": state.Queue()} }
	}
	return &StatusHandler{Monitor: telemetry.New(sources)}
}

func (handler *StatusHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/status", handler.get)
}

func (handler *StatusHandler) get(ctx *gin.Context) {
	snapshot := handler.Monitor.Snapshot()
	ctx.Header("Cache-Control", "no-store")
	status := http.StatusOK
	if snapshot.MySQL["status"] == "unavailable" {
		status = http.StatusServiceUnavailable
	}
	httpapi.WriteSuccess(ctx, status, snapshot)
}
