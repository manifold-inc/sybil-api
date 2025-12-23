package routers

import (
	"database/sql"

	"sybil-api/internal/handlers/targon"
	"sybil-api/internal/middleware"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func RegisterAdminRoutes(e *echo.Group, wdb *sql.DB, rdb *sql.DB, redisClient *redis.Client, targonAPIKey, targonURL string, log *zap.SugaredLogger) error {
	targonHandler, err := targon.NewTargonHandler(wdb, rdb, redisClient, targonAPIKey, targonURL, log)
	if err != nil {
		return err
	}

	// Create the router (HTTP wrapper) - same pattern as InferenceRouter
	targonRouter := NewTargonRouter(targonHandler)

	umw, err := middleware.GetUserMiddleware()
	if err != nil {
		return err
	}

	requireAdmin := e.Group("", umw.ExtractUser, umw.RequireAdmin)

	// Use router methods (which have correct echo.Context signature)
	requireAdmin.POST("/models", targonRouter.CreateModel)
	requireAdmin.DELETE("/models/:uid", targonRouter.DeleteModel)
	requireAdmin.PATCH("/models", targonRouter.UpdateModel)

	return nil
}
