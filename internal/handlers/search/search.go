package search

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	start := time.Now()
	log := c.Log.With("endpoint", "/v1/search", "request_id", c.Reqid)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		log.Warnw("request failed", "error", err.Error(), "status", http.StatusBadRequest, "duration_ms", time.Since(start).Milliseconds())
		return c.String(http.StatusBadRequest, "failed to read request body")
	}

	var req searchRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		log.Warnw("request failed", "error", err.Error(), "status", http.StatusBadRequest, "duration_ms", time.Since(start).Milliseconds())
		return c.String(http.StatusBadRequest, "invalid JSON format")
	}

	if req.Query == "" {
		log.Warnw("request failed", "error", "query is required", "status", http.StatusBadRequest, "duration_ms", time.Since(start).Milliseconds())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "query is required"})
	}

	log = log.With("query", req.Query)

	searchResults, err := QueryGoogleSearch(s.GoogleService, log, s.GoogleSearchEngineID, req.Query, 1)
	if err != nil {
		log.Errorw("request failed", "error", err.Error(), "status", http.StatusInternalServerError, "duration_ms", time.Since(start).Milliseconds())
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "search failed"})
	}

	// Format context for the model from search results
	context := formatSearchContext(searchResults.Results)

	response := SearchResponse{
		Query:   req.Query,
		Context: context,
		Sources: searchResults.Results,
	}

	log.Infow("request completed",
		"results_count", len(searchResults.Results),
		"status", http.StatusOK,
		"duration_ms", time.Since(start).Milliseconds())

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
