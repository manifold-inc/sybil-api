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

type ChatHistoryRequest struct {
	ChatID   string               `json:"chat_id,omitempty"`
	Messages []shared.ChatMessage `json:"messages"`
	Settings *shared.ChatSettings `json:"settings,omitempty"`
}

func (ir *InferenceRouter) ChatHistory(cc echo.Context) error {
	c := cc.(*ctx.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
	}

	var req ChatHistoryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.LogValues.AddError(errors.Join(errors.New("failed to unmarshal request body"), err))
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	if len(req.Messages) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "messages cannot be empty"})
	}

	settings := req.Settings
	if settings == nil {
		settings = &shared.ChatSettings{}
	}

	if !settings.Stream {
		settings.Stream = true
	}

	setupSSEHeaders(c)
	streamCallback := createStreamCallback(c)

	output, err := ir.ih.Chat(&inferenceRoute.ChatInput{
		ChatID:       req.ChatID,
		Messages:     req.Messages,
		Settings:     settings,
		User:         *c.User,
		RequestID:    c.Reqid,
		Ctx:          c.Request().Context(),
		StreamWriter: streamCallback,
	})
	if err != nil {
		c.LogValues.AddError(err)
		c.LogValues.LogLevel = "ERROR"
		var rerr *shared.RequestError
		if errors.As(err, &rerr) {
			return c.JSON(rerr.StatusCode, shared.OpenAIError{
				Message: rerr.Error(),
				Object:  "error",
				Type:    "RequestError",
				Code:    rerr.StatusCode,
			})
		}
		return c.JSON(http.StatusInternalServerError, shared.OpenAIError{
			Message: "internal server error",
			Object:  "error",
			Type:    "InternalError",
			Code:    http.StatusInternalServerError,
		})
	}

	c.LogValues.InferenceInfo = &ctx.InferenceInfo{
		ModelName:   output.ModelName,
		ModelURL:    output.ModelURL,
		ModelID:     output.ModelID,
		Stream:      true,
		InfMetadata: output.InfMetadata,
	}
	c.LogValues.HistoryID = output.HistoryID

	historyEvent := map[string]any{
		"type": "history_id",
		"id":   output.HistoryID,
	}
	historyJSON, _ := json.Marshal(historyEvent)
	_, _ = fmt.Fprintf(c.Response(), "data: %s\n\n", historyJSON)
	c.Response().Flush()

	return nil
}
