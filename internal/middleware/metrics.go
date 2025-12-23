package middleware

import (
	"fmt"
	"time"

	"sybil-api/internal/ctx"
	"sybil-api/internal/metrics"
	"sybil-api/internal/shared"

	"github.com/aidarkhanov/nanoid"
	"github.com/labstack/echo/v4"
	emw "github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
)

func NewTrackMiddleware(log *zap.SugaredLogger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			reqID, _ := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 28)
			logger := log.With(
				"request_id", "req_"+reqID,
			)
			externalID := c.Request().Header.Get("X-External-Request-Id")
			logger = logger.With("externalid", externalID)

			start := time.Now()
			cc := &ctx.Context{Context: c, Log: logger, Reqid: reqID, LogValues: &ctx.ContextLogValues{RequestID: reqID, ExternalID: externalID, StartTime: start, Path: c.Path()}}
			err := next(cc)
			cc.LogValues.RequestDuration = time.Since(start)
			cc.LogValues.StatusCode = cc.Response().Status
			cc.Log.Infow("end_of_request", zap.Object("log_values", cc.LogValues))
			metrics.ResponseCodes.WithLabelValues(cc.Path(), fmt.Sprintf("%d", cc.Response().Status)).Inc()
			return err
		}
	}
}

func NewRecoverMiddleware(log *zap.SugaredLogger) echo.MiddlewareFunc {
	return emw.RecoverWithConfig(emw.RecoverConfig{
		StackSize: 1 << 10, // 1 KB
		LogErrorFunc: func(c echo.Context, err error, stack []byte) error {
			defer func() {
				_ = log.Sync()
			}()
			log.Errorw("Api Panic", "error", err.Error())
			return c.String(500, shared.ErrInternalServerError.Err.Error())
		},
	})
}
