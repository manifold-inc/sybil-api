package routers

import (
	"database/sql"

	"sybil-api/internal/middleware"
	"sybil-api/internal/routes/targon"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func RegisterAdminRoutes(e *echo.Group, wdb *sql.DB, rdb *sql.DB, redisClient *redis.Client, log *zap.SugaredLogger) error {
	targonManager, err := targon.NewTargonManager(wdb, rdb, redisClient, log)
	if err != nil {
		return err
	}
	umw, err := middleware.GetUserMiddleware()
	if err != nil {
		return err
	}

	requireAdmin := e.Group("", umw.ExtractUser, umw.RequireAdmin)

	requireAdmin.POST("/models", targonManager.CreateModel)
	requireAdmin.DELETE("/models/:uid", targonManager.DeleteModel)
	requireAdmin.PATCH("/models", targonManager.UpdateModel)

	return nil
}
