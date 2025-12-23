// Package ctx
package ctx

import (
	"time"

	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ContextLogValues should only be accessed for logging, and not for
// actual business logic, or any other logic
type ContextLogValues struct {
	UserID         uint64 `json:"user_id,omitempty"`
	Credits        uint64 `json:"credits,omitempty"`
	PlanRequests   uint   `json:"plan_requests,omitempty"`
	AllowOverspend bool   `json:"allow_overspend,omitempty"`
	StoreData      bool   `json:"store_data,omitempty"`
	Role           string `json:"role,omitempty"`

	RequestID  string
	ExternalID string
	StartTime  time.Time

	StatusCode      int
	RequestDuration time.Duration
	Error           error
	Path            string
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
