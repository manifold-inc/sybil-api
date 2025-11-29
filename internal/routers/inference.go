// Package routers
package routers

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"sybil-api/internal/middleware"
	inferenceRoute "sybil-api/internal/handlers/inference"
	"sybil-api/internal/setup"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type InferenceRouter struct {
	im *inferenceRoute.InferenceHandler
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

	inferenceRouter := InferenceRouter{im: inferenceManager}

	v1 := e.Group("v1")
	extractUser := v1.Group("", umw.ExtractUser)
	requireUser := v1.Group("", umw.ExtractUser, umw.RequireUser)

	extractUser.GET("/models", inferenceRouter.GetModels)
	requireUser.POST("/chat/completions", inferenceManager.ChatRequest)
	requireUser.POST("/completions", inferenceManager.CompletionRequest)
	requireUser.POST("/embeddings", inferenceManager.EmbeddingRequest)
	requireUser.POST("/responses", inferenceManager.ResponsesRequest)
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

	models, err := ir.im.ListModels(ctx, userID, logfields)
	if err != nil {
		c.Log.Errorw("Failed to get models", "error", err.Error())
		return cc.String(500, "Failed to get models")
	}

	return c.JSON(200, ModelList{
		Data: models,
	})
}
