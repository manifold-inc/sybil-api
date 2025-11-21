package inference

import (
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

	out, reqErr := im.DoInference(InferenceInput{
		Req:       reqInfo,
		User:      *c.User,
		Ctx:       c.Request().Context(),
		LogFields: logfields,
	})

	if reqErr != nil {
		if reqErr.StatusCode >= 500 && reqErr.Err != nil {
			c.Log.Warnw("Inference error", "error", reqErr.Err.Error())
		}
		message := "inference error"
		if reqErr.Err != nil {
			message = reqErr.Err.Error()
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

	if out.Stream {
		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().WriteHeader(http.StatusOK)

		writeSSEChunks(c, out.Chunks)
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
