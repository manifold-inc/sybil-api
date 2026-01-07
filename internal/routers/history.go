package routers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"sybil-api/internal/ctx"
	inferenceRoute "sybil-api/internal/handlers/inference"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

func (ir *InferenceRouter) CompletionRequestNewHistory(cc echo.Context) error {
	c := cc.(*ctx.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
	}

	setupSSEHeaders(c)
	streamCallback := createStreamCallback(c)

	output, err := ir.ih.CompletionRequestNewHistoryLogic(&inferenceRoute.NewHistoryInput{
		Body:         body,
		User:         *c.User,
		RequestID:    c.Reqid,
		Ctx:          c.Request().Context(),
		StreamWriter: streamCallback, // Pass callback for real-time streaming
	})
	if err != nil {
		c.LogValues.AddError(err)
		c.LogValues.LogLevel = "ERROR"
		return nil
	}

	c.LogValues.InferenceInfo = &ctx.InferenceInfo{
		ModelName:   output.ModelName,
		ModelURL:    output.ModelURL,
		ModelID:     output.ModelID,
		Stream:      output.Stream,
		InfMetadata: output.InfMetadata,
	}

	_, _ = fmt.Fprintf(c.Response(), "data: %s\n\n", output.HistoryIDJSON)
	c.Response().Flush()
	c.LogValues.HistoryID = output.HistoryID

	return nil
}

type UpdateHistoryRequest struct {
	Messages []shared.ChatMessage `json:"messages,omitempty"`
	Settings *shared.ChatSettings `json:"settings,omitempty"`
}

// UpdateHistory is the HTTP handler wrapper for the history update logic
func (ir *InferenceRouter) UpdateHistory(cc echo.Context) error {
	c := cc.(*ctx.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	var req UpdateHistoryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.LogValues.AddError(errors.Join(errors.New("failed to unmarshal req body"), err))
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	historyID := c.Param("history_id")
	c.LogValues.HistoryID = historyID

	logfields := buildLogFields(c, shared.ENDPOINTS.CHAT, map[string]string{"history_id": historyID})

	output, err := ir.ih.UpdateHistoryLogic(&inferenceRoute.UpdateHistoryInput{
		HistoryID: historyID,
		Messages:  req.Messages,
		Settings:  req.Settings,
		UserID:    c.User.UserID,
		Ctx:       c.Request().Context(),
		LogFields: logfields,
	})
	if err != nil {
		c.LogValues.AddError(err)
		var rerr *shared.RequestError
		if errors.As(err, &rerr) {
			return c.JSON(rerr.StatusCode, rerr.Err.Error())
		}
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"message": output.Message,
		"id":      output.HistoryID,
		"user_id": output.UserID,
	})
}
