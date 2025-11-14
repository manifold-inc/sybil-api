package inference

import (
	"net/http"

	"sybil-api/internal/setup"
	"sybil-api/internal/shared"
)

type EchoResponder struct {
	c *setup.Context
}

func NewEchoResponder(c *setup.Context) Responder {
	return &EchoResponder{c: c}
}

func (r *EchoResponder) SendJSON(status int, v any) error {
	return r.c.JSON(status, v)
}

func (r *EchoResponder) SendChunk(data []byte) error {
	_, err := r.c.Response().Write(data)
	return err
}

func (r *EchoResponder) Flush() error {
	r.c.Response().Flush()
	return nil
}

func (r *EchoResponder) SendError(err error) error {
	// Check if it's a RequestError with HTTP status code
	if reqErr, ok := err.(*shared.RequestError); ok {
		return r.c.JSON(reqErr.StatusCode, shared.OpenAIError{
			Message: reqErr.Error(),
			Object:  "error",
			Type:    "InternalError",
			Code:    reqErr.StatusCode,
		})
	}

	// Default to 500 Internal Server Error
	return r.c.JSON(http.StatusInternalServerError, shared.OpenAIError{
		Message: err.Error(),
		Object:  "error",
		Type:    "InternalError",
		Code:    http.StatusInternalServerError,
	})
}

// SetHeader sets a response header
func (r *EchoResponder) SetHeader(key, value string) {
	r.c.Response().Header().Set(key, value)
}
