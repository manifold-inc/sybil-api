package search

import (
	"encoding/json"
	"io"
	"net/http"

	"sybil-api/internal/ctx"

	"github.com/labstack/echo/v4"
)

type classifyRequestBody struct {
	Query string `json:"query"`
}

type ClassifyResponse struct {
	NeedsSearch bool   `json:"needs_search"`
	Query       string `json:"query"`
	Reason      string `json:"reason,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

func (s *SearchManager) Classify(cc echo.Context) error {
	c := cc.(*ctx.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.Log.Warnw("Failed to read request body", "error", err.Error())
		return c.String(http.StatusBadRequest, "failed to read request body")
	}

	var req classifyRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		c.Log.Warnw("Failed to parse request body", "error", err.Error())
		return c.String(http.StatusBadRequest, "invalid JSON format")
	}

	if req.Query == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "query is required"})
	}

	response := ClassifyResponse{
		NeedsSearch: false,
		Query:       req.Query,
		Reason:      "no classifier configured",
		Confidence:  "low",
	}

	if s.ClassifySearch != nil {
		result, err := s.ClassifySearch(req.Query, c.User.UserID)
		if err != nil {
			c.Log.Warnw("Search classification failed", "error", err.Error())
			response.Reason = "classification failed"
		} else {
			response.NeedsSearch = result.NeedsSearch
			response.Reason = result.Reason
			response.Confidence = result.Confidence
		}
	}

	return c.JSON(http.StatusOK, response)
}
