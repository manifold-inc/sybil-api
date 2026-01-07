// Package routers
package routers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"sybil-api/internal/ctx"
	"sybil-api/internal/handlers/inference"
	"sybil-api/internal/middleware"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type InferenceRouter struct {
	ih *inference.InferenceHandler
}

func RegisterInferenceRoutes(e *echo.Group, wdb *sql.DB, rdb *sql.DB, redisClient *redis.Client, log *zap.SugaredLogger, debug bool) (func(), error) {
	inferenceManager, inferenceErr := inference.NewInferenceHandler(wdb, rdb, redisClient, log, debug)
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
	Data []inference.Model `json:"data"`
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
	_, err := ir.Inference(cc, shared.ENDPOINTS.CHAT)
	return err
}

func (ir *InferenceRouter) CompletionRequest(cc echo.Context) error {
	_, err := ir.Inference(cc, shared.ENDPOINTS.COMPLETION)
	return err
}

func (ir *InferenceRouter) EmbeddingRequest(cc echo.Context) error {
	_, err := ir.Inference(cc, shared.ENDPOINTS.EMBEDDING)
	return err
}

func (ir *InferenceRouter) ResponsesRequest(cc echo.Context) error {
	_, err := ir.Inference(cc, shared.ENDPOINTS.RESPONSES)
	return err
}

func (ir *InferenceRouter) Inference(cc echo.Context, endpoint string) (*inference.InferenceOutput, error) {
	c := cc.(*ctx.Context)
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.LogValues.AddError(err)
		return nil, c.JSON(http.StatusBadRequest, shared.OpenAIError{
			Message: "failed to read request body",
			Object:  "error",
			Type:    "BadRequest",
			Code:    http.StatusBadRequest,
		})
	}

	reqInfo, preErr := ir.ih.Preprocess(cc.Request().Context(), inference.PreprocessInput{
		Body:      body,
		User:      *c.User,
		Endpoint:  endpoint,
		RequestID: c.Reqid,
	})

	if preErr != nil {
		c.LogValues.AddError(preErr)
		var rerr *shared.RequestError
		if errors.As(preErr, &rerr) {
			return nil, c.JSON(rerr.StatusCode, shared.OpenAIError{
				Message: rerr.Error(),
				Object:  "error",
				Type:    "InternalError",
				Code:    rerr.StatusCode,
			})
		}
		return nil, c.JSON(500, shared.OpenAIError{
			Message: "internal server error",
			Object:  "error",
			Type:    "InternalError",
			Code:    500,
		})
	}

	var out *inference.InferenceOutput
	var reqErr error
	switch reqInfo.Stream {
	case true:
		out, reqErr = ir.StreamInference(c, reqInfo)
	case false:
		out, reqErr = ir.NonStreamInference(c, reqInfo)
	}

	// This is only the case that an error happens and no headers or data has been
	// sent back. This *should* be a rare case
	if reqErr != nil {
		c.LogValues.AddError(reqErr)
		c.LogValues.LogLevel = "ERROR"
		var rerr *shared.RequestError
		// Unkown error, shouldnt really happen
		if !errors.As(reqErr, &rerr) {
			return nil, c.JSON(500, shared.OpenAIError{
				Message: "unkown internal error",
				Object:  "error",
				Type:    "InternalError",
				Code:    500,
			})
		}
		return nil, c.JSON(rerr.StatusCode, shared.OpenAIError{
			Message: rerr.Error(),
			Object:  "error",
			Type:    "InternalError",
			Code:    rerr.StatusCode,
		})
	}

	// Track all metadata for request
	c.LogValues.InferenceInfo = &ctx.InferenceInfo{
		InfMetadata: out.Metadata,
		ModelName:   reqInfo.Model,
		ModelURL:    reqInfo.ModelMetadata.URL,
		ModelID:     reqInfo.ModelMetadata.ModelID,
		Stream:      reqInfo.Stream,
	}
	c.LogValues.AddError(out.Error)
	if out.Error != nil {
		c.LogValues.LogLevel = "ERROR"
	}

	return out, nil
}

func setupSSEHeaders(c *ctx.Context) {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
}

func createStreamCallback(c *ctx.Context) func(token string) error {
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

func (ir *InferenceRouter) StreamInference(c *ctx.Context, reqInfo *inference.RequestInfo) (*inference.InferenceOutput, error) {
	setupSSEHeaders(c)
	streamCallback := createStreamCallback(c)

	out, reqErr := ir.ih.DoInference(inference.InferenceInput{
		Req:          reqInfo,
		User:         *c.User,
		Ctx:          c.Request().Context(),
		StreamWriter: streamCallback, // Pass the callback for real-time streaming
	})
	return out, reqErr
}

func (ir *InferenceRouter) NonStreamInference(c *ctx.Context, reqInfo *inference.RequestInfo) (*inference.InferenceOutput, error) {
	out, reqErr := ir.ih.DoInference(inference.InferenceInput{
		Req:  reqInfo,
		User: *c.User,
		Ctx:  c.Request().Context(),
	})

	if reqErr != nil {
		return out, reqErr
	}

	// Need to actually send back response for non streaming requests
	c.Response().Header().Set("Content-Type", "application/json")
	c.Response().WriteHeader(http.StatusOK)
	if _, err := c.Response().Write(out.FinalResponse); err != nil {
		c.LogValues.AddError(errors.Join(errors.New("failed writing final response"), err))
		c.LogValues.LogLevel = "ERROR"
		return out, err
	}
	return out, reqErr
}
