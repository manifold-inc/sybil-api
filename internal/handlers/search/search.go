package search

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"sybil-api/internal/ctx"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

type searchRequestBody struct {
	Query string `json:"query"`
}

type SearchResponse struct {
	Query   string                 `json:"query"`
	Context string                 `json:"context"`
	Sources []shared.SearchResults `json:"sources"`
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

	if req.Query == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "query is required"})
	}

	searchResults, err := QueryGoogleSearch(s.GoogleService, c.Log, s.GoogleSearchEngineID, req.Query, 1)
	if err != nil {
		c.Log.Errorw("Google search failed", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "search failed"})
	}

	// Format context for the model from search results
	context := formatSearchContext(searchResults.Results)

	response := SearchResponse{
		Query:   req.Query,
		Context: context,
		Sources: searchResults.Results,
	}

	return c.JSON(http.StatusOK, response)
}

// formatSearchContext creates a formatted string from search results
// that can be used as context for an LLM
func formatSearchContext(results []shared.SearchResults) string {
	if len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Search Results:\n\n")

	for i, result := range results {
		sb.WriteString(fmt.Sprintf("[%d] ", i+1))

		if result.Title != nil && *result.Title != "" {
			sb.WriteString(*result.Title)
			sb.WriteString("\n")
		}

		if result.URL != nil && *result.URL != "" {
			sb.WriteString("URL: ")
			sb.WriteString(*result.URL)
			sb.WriteString("\n")
		}

		if result.Content != nil && *result.Content != "" {
			sb.WriteString(*result.Content)
			sb.WriteString("\n")
		}

		if result.Metadata != nil && *result.Metadata != "" {
			sb.WriteString(*result.Metadata)
			sb.WriteString("\n")
		}

		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}
