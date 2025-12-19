package routers

import (
	"context"
	"database/sql"
	"time"

	"sybil-api/internal/handlers/inference"
	"sybil-api/internal/handlers/search"
	"sybil-api/internal/middleware"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
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
	wdb *sql.DB,
	rdb *sql.DB,
	redisClient *redis.Client,
	log *zap.SugaredLogger,
	debug bool,
	config SearchRouterConfig,
) (func(), error) {
	inferenceHandler, err := inference.NewInferenceHandler(wdb, rdb, redisClient, log, debug)
	if err != nil {
		return nil, err
	}

	umw, err := middleware.GetUserMiddleware()
	if err != nil {
		return nil, err
	}

	classifyFunc := func(query string, userID uint64) (*search.ClassifyResult, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		result, err := inferenceHandler.ClassifyNeedsSearch(ctx, inference.ClassifyInput{
			Query:  query,
			UserID: userID,
			LogFields: map[string]string{
				"source": "search_classifier",
			},
		})
		if err != nil {
			log.Warnw("Classification error, defaulting to no search", "error", err)
			return &search.ClassifyResult{
				NeedsSearch: false,
				Reason:      "classification error",
				Confidence:  "low",
			}, nil
		}

		log.Infow("Search classification result",
			"query", query,
			"needs_search", result.NeedsSearch,
			"reason", result.Reason,
			"confidence", result.Confidence,
		)

		return &search.ClassifyResult{
			NeedsSearch: result.NeedsSearch,
			Reason:      result.Reason,
			Confidence:  result.Confidence,
		}, nil
	}

	searchManager, err := search.NewSearchManager(
		config.GoogleSearchEngineID,
		config.GoogleAPIKey,
		config.GoogleACURL,
	)
	if err != nil {
		return nil, err
	}

	searchManager.ClassifySearch = classifyFunc

	searchRouter := &SearchRouter{sm: searchManager}

	searchGroup := e.Group("/v1/search", umw.ExtractUser, umw.RequireUser)
	searchGroup.POST("/classify", searchRouter.sm.Classify)
	searchGroup.POST("", searchRouter.sm.Search)
	searchGroup.POST("/images", searchRouter.sm.GetImages)
	searchGroup.POST("/sources", searchRouter.sm.GetSources)
	searchGroup.GET("/autocomplete", searchRouter.sm.GetAutocomplete)

	return inferenceHandler.ShutDown, nil
}
