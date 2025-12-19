package search

import (
	"encoding/json"
	"io"
	"net/http"

	"sybil-api/internal/ctx"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

type searchRequestBody struct {
	Query string `json:"query"`
}

type SearchResponse struct {
	NeedsSearch bool                   `json:"needs_search"`
	Query       string                 `json:"query"`
	Sources     []shared.SearchResults `json:"sources"`
	Suggestions []string               `json:"suggestions"`
}

func (s *SearchManager) Search(cc echo.Context) error {
	c := cc.(*ctx.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.Log.Warnw("Failed to read request body", "error", err.Error())
		return c.String(http.StatusBadRequest, "failed to read request body")
	}

	var req searchRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		c.Log.Warnw("Failed to parse request body", "error", err.Error())
		return c.String(http.StatusBadRequest, "invalid JSON format")
	}

	needsSearch := false
	if s.ClassifySearch != nil {
		var classifyErr error
		needsSearch, classifyErr = s.ClassifySearch(req.Query, c.User.UserID)
		if classifyErr != nil {
			c.Log.Warnw("Search classification failed, defaulting to no search", "error", classifyErr.Error())
			needsSearch = false
		}
	}

	response := SearchResponse{
		NeedsSearch: needsSearch,
		Query:       req.Query,
		Sources:     []shared.SearchResults{},
		Suggestions: []string{},
	}

	if needsSearch {
		searchResults, err := QueryGoogleSearch(s.GoogleService, c.Log, s.GoogleSearchEngineID, req.Query, 1)
		if err != nil {
			c.Log.Errorw("Google search failed", "error", err.Error())
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "search failed"})
		}
		response.Sources = searchResults.Results
		response.Suggestions = searchResults.Suggestions
	}

	return c.JSON(http.StatusOK, response)
}
