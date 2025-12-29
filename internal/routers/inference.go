// Package routers
package routers

import (
	"context"
	"database/sql"
	"errors"
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

	reqInfo, preErr := ir.ih.Preprocess(inferenceRoute.PreprocessInput{
		Body:      body,
		User:      *c.User,
		Endpoint:  endpoint,
		RequestID: c.Reqid,
	})

	if preErr != nil {
		c.LogValues.AddError(preErr)
		var rerr *shared.RequestError
		if errors.As(err, &rerr) {
			return c.JSON(rerr.StatusCode, shared.OpenAIError{
				Message: rerr.Error(),
				Object:  "error",
				Type:    "InternalError",
				Code:    rerr.StatusCode,
			})
		}
		return c.JSON(500, shared.OpenAIError{
			Message: "internal server error",
			Object:  "error",
			Type:    "InternalError",
			Code:    500,
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
	c.LogValues.AddError(out.Error)
	if out.Error != nil {
		c.LogValues.LogLevel = "ERROR"
	}

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
