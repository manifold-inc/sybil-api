package search

import (
	"encoding/json"
	"io"
	"net/http"

	"api.go/internal/setup"
	"github.com/labstack/echo/v4"
)

type sourcesRequestBody struct {
	Query string `json:"query"`
	Page  int    `json:"page"`
}

func (s *SearchManager) GetSources(cc echo.Context) error {
	c := cc.(*setup.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.Log.Warnw("Failed to read request body", "error", err.Error())
		return c.String(http.StatusBadRequest, "failed to read request body")
	}

	var req sourcesRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		c.Log.Warnw("Failed to parse request body", "error", err.Error())
		return c.String(http.StatusBadRequest, "invalid JSON format")
	}

	c.Log.Infow("/search/sources: %s, page %d\n", req.Query, req.Page)
	search, err := QueryGoogleSearch(s.GoogleService, c.Log, s.GoogleSearchEngineID, req.Query, req.Page)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, search.Results)
}
