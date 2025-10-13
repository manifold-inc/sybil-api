// Package auth defines middleware route based authentication
package auth

import (
	"database/sql"

	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type UserManager struct {
	redis *redis.Client
	rdb   *sql.DB
	log   *zap.SugaredLogger
}

func NewUserManager(r *redis.Client, rdb *sql.DB, log *zap.SugaredLogger) *UserManager {
	return &UserManager{
		redis: r,
		rdb:   rdb,
		log:   log,
	}
}

func (u *UserManager) ExtractUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(cc echo.Context) error {
		c := cc.(*setup.Context)
		c.User = nil

		apiKey, err := shared.ExtractAPIKey(c)
		if err != nil {
			return next(c)
		}
		user, err := u.getUserMetadataFromKey(apiKey, c.Request().Context())
		if err != nil {
			return next(c)
		}
		c.User = user
		c.Log = c.Log.With("user_id", c.User.UserID)
		return next(c)
	}
}

func (u *UserManager) RequireUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(cc echo.Context) error {
		c := cc.(*setup.Context)
		if c.User == nil {
			return c.String(401, "unauthorized")
		}
		return next(c)
	}
}

func (u *UserManager) RequireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(cc echo.Context) error {
		c := cc.(*setup.Context)
		if c.User == nil || c.User.Role != "admin" {
			return c.String(401, "unauthorized")
		}
		return next(c)
	}
}
