// Package ctx
package ctx

import (
	"fmt"
	"time"

	"sybil-api/internal/handlers/inference"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type InferenceInfo struct {
	ModelName   string
	ModelURL    string
	ModelID     uint64
	Stream      bool
	InfMetadata *inference.InferenceMetadata
}

// ContextLogValues should only be accessed for logging, and not for
// actual business logic, or any other logic
type ContextLogValues struct {
	// Added in base middleware
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

	// Inference metadata fields
	InferenceInfo *InferenceInfo

	// History related
	HistoryID string

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
	if c.InferenceInfo != nil {
		enc.AddBool("stream", c.InferenceInfo.Stream)
		enc.AddString("model_url", c.InferenceInfo.ModelURL)
		enc.AddUint64("model_id", c.InferenceInfo.ModelID)
		enc.AddString("model_name", c.InferenceInfo.ModelName)
		if c.InferenceInfo.InfMetadata != nil {
			enc.AddDuration("ttft", c.InferenceInfo.InfMetadata.TimeToFirstToken)
			enc.AddDuration("total_time", c.InferenceInfo.InfMetadata.TotalTime)
			enc.AddBool("completed", c.InferenceInfo.InfMetadata.Completed)
			enc.AddBool("canceled", c.InferenceInfo.InfMetadata.Canceled)
		}
	}
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
