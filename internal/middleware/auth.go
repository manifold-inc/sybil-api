// Package middleware defines middleware route based authentication
package middleware

import (
	"database/sql"
	"errors"
	"sync"

	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type UserMiddleware struct {
	redis *redis.Client
	rdb   *sql.DB
	log   *zap.SugaredLogger
}

var (
	userManager      *UserMiddleware
	userManagerMutex sync.Mutex
)

func InitUserMiddleware(r *redis.Client, rdb *sql.DB, log *zap.SugaredLogger) {
	userManagerMutex.Lock()
	defer userManagerMutex.Unlock()
	um := NewUserMiddleware(r, rdb, log)
	userManager = um
}

func GetUserMiddleware() (*UserMiddleware, error) {
	userManagerMutex.Lock()
	defer userManagerMutex.Unlock()
	if userManager == nil {
		return nil, errors.New("user manager not initalized")
	}
	return userManager, nil
}

func NewUserMiddleware(r *redis.Client, rdb *sql.DB, log *zap.SugaredLogger) *UserMiddleware {
	return &UserMiddleware{
		redis: r,
		rdb:   rdb,
		log:   log,
	}
}

func (u *UserMiddleware) ExtractUser(next echo.HandlerFunc) echo.HandlerFunc {
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

func (u *UserMiddleware) RequireUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(cc echo.Context) error {
		c := cc.(*setup.Context)
		if c.User == nil {
			return c.String(401, "unauthorized")
		}
		return next(c)
	}
}

func (u *UserMiddleware) RequireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(cc echo.Context) error {
		c := cc.(*setup.Context)
		if c.User == nil || c.User.Role != "ADMIN" {
			return c.String(401, "unauthorized")
		}
		return next(c)
	}
}
