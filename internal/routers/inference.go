// Package routers
package routers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"sybil-api/internal/ctx"
	inferenceRoute "sybil-api/internal/handlers/inference"
	"sybil-api/internal/middleware"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type InferenceRouter struct {
	ih *inferenceRoute.InferenceHandler
}

func RegisterInferenceRoutes(e *echo.Group, wdb *sql.DB, rdb *sql.DB, redisClient *redis.Client, log *zap.SugaredLogger, debug bool) (func(), error) {
	inferenceManager, inferenceErr := inferenceRoute.NewInferenceHandler(wdb, rdb, redisClient, log, debug)
	if inferenceErr != nil {
		return nil, inferenceErr
	}
	defer inferenceManager.ShutDown()
	umw, err := middleware.GetUserMiddleware()
	if err != nil {
		return nil, err
	}

	inferenceRouter := InferenceRouter{ih: inferenceManager}

	v1 := e.Group("v1")
	extractUser := v1.Group("", umw.ExtractUser)
	requireUser := v1.Group("", umw.ExtractUser, umw.RequireUser)

	extractUser.GET("/models", inferenceRouter.GetModels)
	requireUser.POST("/chat/completions", inferenceRouter.ChatRequest)
	requireUser.POST("/completions", inferenceRouter.CompletionRequest)
	requireUser.POST("/embeddings", inferenceRouter.EmbeddingRequest)
	requireUser.POST("/responses", inferenceRouter.ResponsesRequest)
	requireUser.POST("/chat/history/new", inferenceRouter.CompletionRequestNewHistory)
	requireUser.PATCH("/chat/history/:history_id", inferenceRouter.UpdateHistory)
	return inferenceManager.ShutDown, nil
}

type ModelList struct {
	Data []inferenceRoute.Model `json:"data"`
}

func (ir *InferenceRouter) GetModels(cc echo.Context) error {
	c := cc.(*ctx.Context)

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	var userID *uint64
	if c.User != nil {
		userID = &c.User.UserID
	}

	models, err := ir.ih.ListModels(ctx, userID)
	if err != nil {
		c.LogValues.AddError(errors.Join(errors.New("failed to get models"), err))
		return cc.String(500, "Failed to get models")
	}

	return c.JSON(200, ModelList{
		Data: models,
	})
}

func (ir *InferenceRouter) ChatRequest(cc echo.Context) error {
	err := ir.Inference(cc, shared.ENDPOINTS.CHAT)
	return err
}

func (ir *InferenceRouter) CompletionRequest(cc echo.Context) error {
	err := ir.Inference(cc, shared.ENDPOINTS.COMPLETION)
	return err
}

func (ir *InferenceRouter) EmbeddingRequest(cc echo.Context) error {
	err := ir.Inference(cc, shared.ENDPOINTS.EMBEDDING)
	return err
}

func (ir *InferenceRouter) ResponsesRequest(cc echo.Context) error {
	err := ir.Inference(cc, shared.ENDPOINTS.RESPONSES)
	return err
}

func (ir *InferenceRouter) Inference(cc echo.Context, endpoint string) error {
	c := cc.(*ctx.Context)
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.LogValues.AddError(err)
		return c.JSON(http.StatusBadRequest, shared.OpenAIError{
			Message: "failed to read request body",
			Object:  "error",
			Type:    "BadRequest",
			Code:    http.StatusBadRequest,
		})
	}

	logfields := buildLogFields(c, endpoint, nil)

	reqInfo, preErr := ir.ih.Preprocess(inferenceRoute.PreprocessInput{
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
		c.LogValues.AddError(preErr)
		return c.JSON(preErr.StatusCode, shared.OpenAIError{
			Message: message,
			Object:  "error",
			Type:    "InternalError",
			Code:    preErr.StatusCode,
		})
	}

	var streamCallback func(token string) error
	if reqInfo.Stream {
		setupSSEHeaders(c)
		streamCallback = createStreamCallback(c)
	}

	out, reqErr := ir.ih.DoInference(inferenceRoute.InferenceInput{
		Req:          reqInfo,
		User:         *c.User,
		Ctx:          c.Request().Context(),
		LogFields:    logfields,
		StreamWriter: streamCallback, // Pass the callback for real-time streaming
	})

	// This is only the case that an error happens and no headers or data has been
	// sent back. This *should* be a rare case
	if reqErr != nil {
		c.LogValues.AddError(reqErr)
		c.LogValues.LogLevel = "ERROR"
		var rerr *shared.RequestError
		// Unkown error, shouldnt really happen
		if !errors.As(reqErr, &rerr) {
			return c.JSON(500, shared.OpenAIError{
				Message: "unkown internal error",
				Object:  "error",
				Type:    "InternalError",
				Code:    500,
			})
		}
		return c.JSON(rerr.StatusCode, shared.OpenAIError{
			Message: rerr.Error(),
			Object:  "error",
			Type:    "InternalError",
			Code:    rerr.StatusCode,
		})
	}

	// Track all metadata for request
	c.LogValues.InfMetadata = out.Metadata

	// Need to actually send back response for non streaming requests
	if !out.Metadata.Stream {
		c.Response().Header().Set("Content-Type", "application/json")
		c.Response().WriteHeader(http.StatusOK)
		if _, err := c.Response().Write(out.FinalResponse); err != nil {
			c.LogValues.AddError(errors.Join(errors.New("failed writing final response"), err))
			c.LogValues.LogLevel = "ERROR"
			return err
		}
	}

	return nil
}

func (ir *InferenceRouter) CompletionRequestNewHistory(cc echo.Context) error {
	c := cc.(*ctx.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
	}

	logfields := buildLogFields(c, shared.ENDPOINTS.CHAT, nil)

	setupSSEHeaders(c)
	streamCallback := createStreamCallback(c)

	// TODO @sean this function needs refactored to return inference metadata in some capacity
	// so it can be added to logs
	output, err := ir.ih.CompletionRequestNewHistoryLogic(&inferenceRoute.NewHistoryInput{
		Body:         body,
		User:         *c.User,
		RequestID:    c.Reqid,
		Ctx:          c.Request().Context(),
		LogFields:    logfields,
		StreamWriter: streamCallback, // Pass callback for real-time streaming
	})
	if err != nil {
		c.LogValues.AddError(err)
		c.LogValues.LogLevel = "ERROR"
		return nil
	}

	_, _ = fmt.Fprintf(c.Response(), "data: %s\n\n", output.HistoryIDJSON)
	c.Response().Flush()
	c.LogValues.HistoryID = output.HistoryID

	// TODO @sean why is this here, this makes no sense
	if !output.Stream && len(output.FinalResponse) > 0 {
		_, _ = fmt.Fprintf(c.Response(), "data: %s\n\n", string(output.FinalResponse))
		c.Response().Flush()
	}

	return nil
}

type UpdateHistoryRequest struct {
	Messages []shared.ChatMessage `json:"messages,omitempty"`
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
