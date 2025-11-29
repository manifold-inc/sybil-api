// Package routers
package routers

import (
	"database/sql"

	"sybil-api/internal/middleware"
	inferenceRoute "sybil-api/internal/routes/inference"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func RegisterInferenceRoutes(e *echo.Group, wdb *sql.DB, rdb *sql.DB, redisClient *redis.Client, log *zap.SugaredLogger, debug bool) (func(), error) {
	inferenceManager, inferenceErr := inferenceRoute.NewInferenceManager(wdb, rdb, redisClient, log, debug)
	if inferenceErr != nil {
		return nil, inferenceErr
	}
	defer inferenceManager.ShutDown()
	umw, err := middleware.GetUserMiddleware()
	if err != nil {
		return nil, err
	}
	v1 := e.Group("v1")
	extractUser := v1.Group("", umw.ExtractUser)
	requireUser := v1.Group("", umw.ExtractUser, umw.RequireUser)

	extractUser.GET("/models", inferenceManager.Models)
	requireUser.POST("/chat/completions", inferenceManager.ChatRequest)
	requireUser.POST("/completions", inferenceManager.CompletionRequest)
	requireUser.POST("/embeddings", inferenceManager.EmbeddingRequest)
	requireUser.POST("/responses", inferenceManager.ResponsesRequest)
	requireUser.POST("/chat/history/new", inferenceManager.CompletionRequestNewHistory)
	requireUser.PATCH("/chat/history/:history_id", inferenceManager.UpdateHistory)
	return inferenceManager.ShutDown, nil
}
