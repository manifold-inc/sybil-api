package middleware

import (
	"fmt"
	"time"

	"sybil-api/internal/metrics"
	"sybil-api/internal/setup"
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
			logger = logger.With("externalid", c.Request().Header.Get("X-Dippy-Request-Id"))

			cc := &setup.Context{Context: c, Log: logger, Reqid: reqID}
			start := time.Now()
			err := next(cc)
			duration := time.Since(start)
			cc.Log.Infow("end_of_request", "status_code", fmt.Sprintf("%d", cc.Response().Status), "duration", duration.String())
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
