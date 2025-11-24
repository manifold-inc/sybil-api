package inference

import (
	"fmt"
	"net/http"

	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

func (im *InferenceManager) HandleInferenceHTTP(cc echo.Context, endpoint string) (string, error) {
	c := cc.(*setup.Context)
	body, err := readRequestBody(c)
	if err != nil {
		return "", c.JSON(http.StatusBadRequest, shared.OpenAIError{
			Message: "failed to read request body",
			Object:  "error",
			Type:    "BadRequest",
			Code:    http.StatusBadRequest,
		})
	}

	logfields := buildLogFields(c, endpoint, nil)

	reqInfo, preErr := im.Preprocess(PreprocessInput{
		Body:      body,
		User:      *c.User,
		Endpoint:  endpoint,
		RequestID: c.Reqid,
		LogFields: logfields,
	})
	if preErr != nil {
		message := "inference error"
		if preErr.Err != nil {
			message = preErr.Err.Error()
		}
		return "", c.JSON(preErr.StatusCode, shared.OpenAIError{
			Message: message,
			Object:  "error",
			Type:    "InternalError",
			Code:    preErr.StatusCode,
		})
	}

	// Create streaming callback for real-time token delivery
	var streamCallback func(token string) error
	if reqInfo.Stream {
		// Set SSE headers before streaming starts
		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().WriteHeader(http.StatusOK)

		streamCallback = func(token string) error {
			// Check if client disconnected
			if c.Request().Context().Err() != nil {
				return c.Request().Context().Err()
			}
			// Write token immediately to client
			_, err := fmt.Fprintf(c.Response(), "%s\n\n", token)
			if err != nil {
				return err
			}
			c.Response().Flush()
			return nil
		}
	}

	out, reqErr := im.DoInference(InferenceInput{
		Req:          reqInfo,
		User:         *c.User,
		Ctx:          c.Request().Context(),
		LogFields:    logfields,
		StreamWriter: streamCallback, // Pass the callback for real-time streaming
	})

	if reqErr != nil {
		if reqErr.StatusCode >= 500 && reqErr.Err != nil {
			c.Log.Warnw("Inference error", "error", reqErr.Err.Error())
		}
		message := "inference error"
		if reqErr.Err != nil {
			message = reqErr.Err.Error()
		}
		
		// If streaming already started, we can't send JSON error
		if reqInfo.Stream {
			c.Log.Errorw("Error after streaming started", "error", message)
			return "", nil
		}
		
		return "", c.JSON(reqErr.StatusCode, shared.OpenAIError{
			Message: message,
			Object:  "error",
			Type:    "InternalError",
			Code:    reqErr.StatusCode,
		})
	}

	if out == nil {
		return "", nil
	}

	// For streaming, response already sent via callback
	if out.Stream {
		return string(out.FinalResponse), nil
	}

	c.Response().Header().Set("Content-Type", "application/json")
	c.Response().WriteHeader(http.StatusOK)
	if _, err := c.Response().Write(out.FinalResponse); err != nil {
		c.Log.Errorw("Failed to write response", "error", err)
		return "", err
	}
	return string(out.FinalResponse), nil
}
