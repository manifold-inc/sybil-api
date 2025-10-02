package search

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"sybil-api/internal/setup"

	"github.com/labstack/echo/v4"
)

func (s *SearchManager) GetAutocomplete(cc echo.Context) error {
	c := cc.(*setup.Context)
	query := c.QueryParam("q")
	if query == "" {
		return c.JSON(http.StatusOK, []string{})
	}

	suggestions, err := queryGoogleAutocomplete(c, s.GoogleACURL, query)
	if err != nil {
		return c.String(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, suggestions)
}

func queryGoogleAutocomplete(c *setup.Context, googleACURL string, query string) ([]any, error) {
	// google autocomplete endpoint
	url := fmt.Sprintf("%s?client=firefox&q=%s", googleACURL, url.QueryEscape(query))

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		c.Log.Errorf("Failed to create autocomplete request: %s", err.Error())
		return nil, err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		c.Log.Errorf("Autocomplete Error: %s", err.Error())
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		c.Log.Errorf("Autocomplete Error. Status code: %d", res.StatusCode)
		return nil, fmt.Errorf("autocomplete failed with status: %d", res.StatusCode)
	}

	resBody, err := io.ReadAll(res.Request.Body)
	if err != nil {
		c.Log.Warnw("Failed to read response body", "error", err.Error())
	}

	// Google's response is in JSON format: [query, [suggestions], [], metadata]
	var response []any
	if err := json.Unmarshal(resBody, &response); err != nil {
		c.Log.Warnw("Failed to parse request body", "error", err.Error())
		return nil, nil
	}

	// Return only the first two elements: [query, [suggestions]]
	if len(response) >= 2 {
		return []any{response[0], response[1]}, nil
	}

	// If response is malformed, return empty query and suggestions
	return []any{"", []string{}}, nil
}
