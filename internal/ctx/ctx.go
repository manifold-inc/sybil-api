// Package ctx
package ctx

import (
	"fmt"
	"time"

	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ContextLogValues should only be accessed for logging, and not for
// actual business logic, or any other logic
type ContextLogValues struct {
	// Added in base middleware
	RequestID       string
	ExternalID      string
	StartTime       time.Time
	StatusCode      int
	RequestDuration time.Duration
	Path            string

	// Added in user middleware
	UserID         uint64
	Credits        uint64
	PlanRequests   uint
	AllowOverspend bool
	StoreData      bool
	Role           string

	// Override log Log Level
	// useful for streaming where status code might be sent before errors from
	// mid-stream or post processing occur
	LogLevel string

	// Added dynamically
	Error error
}

// AddError adds errors to the error chain. Always add errors, even if only warnings.
// Log level is determined by the status code of the reuqest
func (c *ContextLogValues) AddError(err error) {
	if c.Error == nil {
		c.Error = err
		return
	}
	c.Error = fmt.Errorf("%w: %w", err, c.Error)
}

func (c *ContextLogValues) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	if c.UserID != 0 {
		enc.AddUint64("user_id", c.UserID)
		enc.AddUint64("credits", c.Credits)
		enc.AddUint("plan_requests", c.PlanRequests)
		enc.AddBool("allow_overspend", c.AllowOverspend)
		enc.AddBool("store_data", c.StoreData)
		enc.AddString("role", c.Role)
	}
	enc.AddString("request_id", c.RequestID)
	enc.AddString("external_id", c.ExternalID)
	enc.AddTime("start_time", c.StartTime)
	enc.AddDuration("request_duration", c.RequestDuration)
	enc.AddInt("status_code", c.StatusCode)
	if c.Error != nil {
		enc.AddString("error", c.Error.Error())
	}
	enc.AddString("path", c.Path)
	return nil
}

type Context struct {
	echo.Context
	Log       *zap.SugaredLogger
	Reqid     string
	User      *shared.UserMetadata
	LogValues *ContextLogValues
}
