package inference

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

func (im *InferenceManager) ChatRequest(cc echo.Context) error {
	_, err := im.HandleInferenceHTTP(cc, shared.ENDPOINTS.CHAT)
	return err
}

func (im *InferenceManager) CompletionRequest(cc echo.Context) error {
	_, err := im.HandleInferenceHTTP(cc, shared.ENDPOINTS.COMPLETION)
	return err
}

func (im *InferenceManager) EmbeddingRequest(cc echo.Context) error {
	_, err := im.HandleInferenceHTTP(cc, shared.ENDPOINTS.EMBEDDING)
	return err
}

func (im *InferenceManager) ResponsesRequest(cc echo.Context) error {
	_, err := im.HandleInferenceHTTP(cc, shared.ENDPOINTS.RESPONSES)
	return err
}

// CompletionRequestNewHistory is the HTTP handler wrapper for the history creation logic
func (im *InferenceManager) CompletionRequestNewHistory(cc echo.Context) error {
	c := cc.(*setup.Context)

	// Read HTTP body
	body, err := readRequestBody(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
	}

	logfields := buildLogFields(c, shared.ENDPOINTS.CHAT, nil)

	// Call pure logic function (note: renamed internal function to avoid conflict)
	output, err := im.completionRequestNewHistoryLogic(&NewHistoryInput{
		Body:      body,
		User:      *c.User,
		RequestID: c.Reqid,
		Ctx:       c.Request().Context(),
		LogFields: logfields,
	})

	if err != nil {
		c.Log.Errorw("History creation failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}

	// Handle errors from logic layer
	if output.Error != nil {
		return c.JSON(output.Error.StatusCode, map[string]string{"error": output.Error.Message})
	}

	// Set up SSE response
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().WriteHeader(http.StatusOK)

	// Write history ID event
	fmt.Fprintf(c.Response(), "data: %s\n\n", output.HistoryIDJSON)
	c.Response().Flush()

	// Write inference chunks or final response
	if output.Stream {
		writeSSEChunks(c, output.StreamChunks)
	} else if len(output.FinalResponse) > 0 {
		// For non-streaming, still need to write as SSE since we already set the header
		fmt.Fprintf(c.Response(), "data: %s\n\n", string(output.FinalResponse))
		c.Response().Flush()
	}

	return nil
}

// UpdateHistory is the HTTP handler wrapper for the history update logic
func (im *InferenceManager) UpdateHistory(cc echo.Context) error {
	c := cc.(*setup.Context)

	// Read HTTP body
	body, err := readRequestBody(c)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	// Parse request
	var req UpdateHistoryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.Log.Errorw("Failed to unmarshal request body", "error", err.Error())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	// Get history ID from URL param
	historyID := c.Param("history_id")

	logfields := buildLogFields(c, shared.ENDPOINTS.CHAT, map[string]string{"history_id": historyID})

	// Call pure logic function (note: renamed internal function to avoid conflict)
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

	// Handle errors from logic layer
	if output.Error != nil {
		return c.JSON(output.Error.StatusCode, map[string]string{"error": output.Error.Message})
	}

	// Write success response
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
	for k, v := range extras {
		fields[k] = v
	}
	return fields
}

func writeSSEChunks(c *setup.Context, chunks []string) {
	writer := c.Response().Writer
	flusher, _ := writer.(http.Flusher)

	streamChunks := chunks
	if len(streamChunks) == 0 {
		streamChunks = []string{""}
	}

	for _, chunk := range streamChunks {
		fmt.Fprintf(writer, "data: %s\n\n", chunk)
		if flusher != nil {
			flusher.Flush()
		}
	}
}
