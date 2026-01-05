package middleware

import (
	"errors"
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
			cc := &ctx.Context{Context: c, Log: logger, Reqid: reqID, LogValues: &ctx.ContextLogValues{StartTime: start, Path: c.Path()}}
			err := next(cc)
			cc.LogValues.RequestDuration = time.Since(start)
			status := cc.Response().Status
			cc.LogValues.StatusCode = status

			// Switch cases are top down, so we make sure to check any overrides
			// for log levels (usually from streaming requests) before the presented
			// status code
			switch true {
			case cc.LogValues.LogLevel == "ERROR":
				cc.Log.Errorw("end_of_request", zap.Object("log_values", cc.LogValues))
			case cc.LogValues.LogLevel == "WARN":
				cc.Log.Warnw("end_of_request", zap.Object("log_values", cc.LogValues))
			case cc.LogValues.LogLevel == "INFO":
				cc.Log.Infow("end_of_request", zap.Object("log_values", cc.LogValues))

			case status < 300:
				cc.Log.Infow("end_of_request", zap.Object("log_values", cc.LogValues))
			case status < 500:
				cc.Log.Warnw("end_of_request", zap.Object("log_values", cc.LogValues))
			default:
				cc.Log.Errorw("end_of_request", zap.Object("log_values", cc.LogValues))
			}
			metrics.ResponseCodes.WithLabelValues(cc.Path(), fmt.Sprintf("%d", cc.Response().Status)).Inc()

			modelName := "unknown"
			if cc.LogValues.InferenceInfo != nil {
				modelName = cc.LogValues.InferenceInfo.ModelName
			}
			errs := stackTrace(cc.LogValues.Error)
			for _, err := range errs {
				var e *shared.MetricsError
				if ok := errors.As(err, &e); ok {
					metrics.ErrorCount.WithLabelValues(modelName, cc.LogValues.Path, fmt.Sprintf("%d", cc.LogValues.UserID), e.Code)
				}
			}
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

// stackTrace unwraps all errors from an error chain
// https://erik.cat/blog/error-wrapping-go/
func stackTrace(err error) []error {
	result := make([]error, 0)
	if err == nil {
		return result
	}

	// Unwrap joined errors and ignore the join itself.
	if e, ok := err.(interface {
		Unwrap() []error
	}); ok {
		for _, err := range e.Unwrap() {
			result = append(result, stackTrace(err)...)
		}

		return result
	}

	// We can ignore the wrapped error, as it's contained
	// in the fmt.Errorf string.
	return append(result, err)
}
