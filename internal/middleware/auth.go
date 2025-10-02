// Package auth defines middelware route based authentication
package auth

import (
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"
	"sybil-api/internal/users"

	"github.com/labstack/echo/v4"
)

func ExtractUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(cc echo.Context) error {
		c := cc.(*setup.Context)
		c.User = nil

		apiKey, err := shared.ExtractAPIKey(c)
		if err != nil {
			return next(c)
		}
		user, err := users.GetUserMetadataFromKey(apiKey, c)
		if err != nil {
			return next(c)
		}
		c.User = user
		c.Log = c.Log.With("user_id", c.User.UserID)
		return next(c)
	}
}

func RequireUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(cc echo.Context) error {
		c := cc.(*setup.Context)
		if c.User == nil {
			return c.String(401, "unauthorized")
		}
		return next(c)
	}
}
