// Package routers
package routers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	inferenceRoute "sybil-api/internal/handlers/inference"
	"sybil-api/internal/middleware"
	"sybil-api/internal/setup"
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
	requireUser.POST("/chat/history/new", inferenceManager.CompletionRequestNewHistory)
	requireUser.PATCH("/chat/history/:history_id", inferenceManager.UpdateHistory)
	return inferenceManager.ShutDown, nil
}

type ModelList struct {
	Data []inferenceRoute.Model `json:"data"`
}

func (ir *InferenceRouter) GetModels(cc echo.Context) error {
	c := cc.(*setup.Context)

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	logfields := map[string]string{
		"endpoint": "models",
	}
	if c.User != nil {
		logfields["user_id"] = fmt.Sprintf("%d", c.User.UserID)
	}

	var userID *uint64
	if c.User != nil {
		userID = &c.User.UserID
	}

	models, err := ir.ih.ListModels(ctx, userID, logfields)
	if err != nil {
		c.Log.Errorw("Failed to get models", "error", err.Error())
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

func (ir *InferenceRouter) Inference(cc echo.Context, endpoint string) (string, error) {
	c := cc.(*Context)
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
		return "", c.JSON(preErr.StatusCode, shared.OpenAIError{
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

	if reqErr != nil {
		if reqErr.StatusCode >= 500 && reqErr.Err != nil {
			c.Log.Warnw("Inference error", "error", reqErr.Err.Error())
		}
		message := "inference error"
		if reqErr.Err != nil {
			message = reqErr.Err.Error()
		}

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
