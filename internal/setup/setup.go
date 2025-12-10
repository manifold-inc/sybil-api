// Package setup server
package setup

import (
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

type Context struct {
	echo.Context
	Log   *zap.SugaredLogger
	Reqid string
	User  *shared.UserMetadata
}
