package search

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"sybil-api/internal/setup"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type searchRequestBody struct {
	Query string `json:"query"`
	Model string `json:"model"`
}

func (s *SearchManager) Search(cc echo.Context) error {
	c := cc.(*setup.Context)

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

	c.Request().Header.Add("Content-Type", "application/json")
	c.Response().Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().Header().Set("X-Accel-Buffering", "no")

	general, err := QueryGoogleSearch(s.GoogleService, c.Log, s.GoogleSearchEngineID, req.Query, 1)
	if err != nil {
		return c.String(http.StatusInternalServerError, "")
	}

	sendEvent(c, map[string]any{
		"type":    "sources",
		"sources": general.Results,
	})
	sendEvent(c, map[string]any{
		"type":      "related",
		"followups": general.Suggestions,
	})

	//llmSources := []string{}
	if len(general.Results) != 0 {
		herocard := general.Results[0]
		/* used for query targon
		llmSources = append(llmSources, fmt.Sprintf("Title: %s:\nSnippet: %s\n", shared.DerefString(general.Results[0].Title), shared.DerefString(general.Results[0].Content)))
		*/
		sendEvent(c, map[string]any{
			"type": "heroCard",
			"heroCard": map[string]any{
				"type": "news",
				"url":  *herocard.URL,
				"image": func() any {
					if herocard.Thumbnail != nil && *herocard.Thumbnail != "" {
						return *herocard.Thumbnail
					}
					return nil
				}(),
				"title": *herocard.Title,
				"intro": *herocard.Content,
				"size":  "auto",
			},
		})
	}

	/* TODO: Figure out how to query targon here
	answer := queryTargon(cc, llmSources, query, model) */
	c.Log.Infoln("Finished")
	return c.String(200, "")
}

func sendEvent(c *setup.Context, data map[string]any) {
	eventID := uuid.New().String()
	fmt.Fprintf(c.Response(), "id: %s\n", eventID)
	fmt.Fprintf(c.Response(), "event: new_message\n")
	eventData, _ := json.Marshal(data)
	fmt.Fprintf(c.Response(), "data: %s\n", string(eventData))
	fmt.Fprintf(c.Response(), "retry: %d\n\n", 1500)
	c.Response().Flush()
}
