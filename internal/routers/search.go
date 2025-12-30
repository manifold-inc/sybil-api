package routers

import (
	"sybil-api/internal/handlers/search"
	"sybil-api/internal/middleware"

	"github.com/labstack/echo/v4"
)

type SearchRouter struct {
	sm *search.SearchManager
}

type SearchRouterConfig struct {
	GoogleSearchEngineID string
	GoogleAPIKey         string
	GoogleACURL          string
}

func RegisterSearchRoutes(
	e *echo.Group,
	config SearchRouterConfig,
) error {
	umw, err := middleware.GetUserMiddleware()
	if err != nil {
		return err
	}

	searchManager, err := search.NewSearchManager(
		config.GoogleSearchEngineID,
		config.GoogleAPIKey,
		config.GoogleACURL,
	)
	if err != nil {
		return err
	}

	searchRouter := &SearchRouter{sm: searchManager}

	searchGroup := e.Group("/v1/search", umw.ExtractUser, umw.RequireUser)
	searchGroup.POST("/classify", searchRouter.sm.Classify)
	searchGroup.POST("", searchRouter.sm.Search)
	searchGroup.POST("/images", searchRouter.sm.GetImages)
	searchGroup.POST("/sources", searchRouter.sm.GetSources)
	searchGroup.GET("/autocomplete", searchRouter.sm.GetAutocomplete)

	return nil
}
