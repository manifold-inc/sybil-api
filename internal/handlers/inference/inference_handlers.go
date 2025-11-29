package inference

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"

	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

func (im *InferenceHandler) CompletionRequestNewHistory(cc echo.Context) error {
	c := cc.(*setup.Context)

	body, err := readRequestBody(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
	}

	logfields := buildLogFields(c, shared.ENDPOINTS.CHAT, nil)

	setupSSEHeaders(c)
	streamCallback := createStreamCallback(c)

	output, err := im.completionRequestNewHistoryLogic(&NewHistoryInput{
		Body:         body,
		User:         *c.User,
		RequestID:    c.Reqid,
		Ctx:          c.Request().Context(),
		LogFields:    logfields,
		StreamWriter: streamCallback, // Pass callback for real-time streaming
	})
	if err != nil {
		c.Log.Errorw("History creation failed", "error", err)
		return nil
	}

	if output.Error != nil {
		c.Log.Errorw("History logic error", "error", output.Error.Message)
		return nil
	}

	_, _ = fmt.Fprintf(c.Response(), "data: %s\n\n", output.HistoryIDJSON)
	c.Response().Flush()

	if !output.Stream && len(output.FinalResponse) > 0 {
		_, _ = fmt.Fprintf(c.Response(), "data: %s\n\n", string(output.FinalResponse))
		c.Response().Flush()
	}

	return nil
}

// UpdateHistory is the HTTP handler wrapper for the history update logic
func (im *InferenceHandler) UpdateHistory(cc echo.Context) error {
	c := cc.(*setup.Context)

	body, err := readRequestBody(c)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	var req UpdateHistoryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.Log.Errorw("Failed to unmarshal request body", "error", err.Error())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	historyID := c.Param("history_id")

	logfields := buildLogFields(c, shared.ENDPOINTS.CHAT, map[string]string{"history_id": historyID})

	output, err := im.updateHistoryLogic(&UpdateHistoryInput{
		HistoryID: historyID,
		Messages:  req.Messages,
		UserID:    c.User.UserID,
		Ctx:       c.Request().Context(),
		LogFields: logfields,
	})
	if err != nil {
		c.Log.Errorw("History update failed", "error", err)
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	if output.Error != nil {
		return c.JSON(output.Error.StatusCode, map[string]string{"error": output.Error.Message})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"message": output.Message,
		"id":      output.HistoryID,
		"user_id": output.UserID,
	})
}

func readRequestBody(c *setup.Context) ([]byte, error) {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.Log.Errorw("Failed to read request body", "error", err.Error())
		return nil, err
	}
	return body, nil
}

func buildLogFields(c *setup.Context, endpoint string, extras map[string]string) map[string]string {
	fields := map[string]string{
		"endpoint":   endpoint,
		"user_id":    fmt.Sprintf("%d", c.User.UserID),
		"request_id": c.Reqid,
	}
	maps.Copy(fields, extras)
	return fields
}

func setupSSEHeaders(c *setup.Context) {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
}

func createStreamCallback(c *setup.Context) func(token string) error {
	return func(token string) error {
		if c.Request().Context().Err() != nil {
			return c.Request().Context().Err()
		}
		_, err := fmt.Fprintf(c.Response(), "%s\n\n", token)
		if err != nil {
			return err
		}
		c.Response().Flush()
		return nil
	}
}
