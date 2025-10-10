package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

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

	llmSources := []string{}
	if len(general.Results) != 0 {
		herocard := general.Results[0]
		llmSources = append(llmSources, fmt.Sprintf("Title: %s:\nSnippet: %s\n", shared.DerefString(general.Results[0].Title), shared.DerefString(general.Results[0].Content)))
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

	// Build inference request
	now := time.Now()
	messages := []shared.ChatMessage{
		{
			Role: "system",
			Content: fmt.Sprintf(`### Current Date: %s
### Instruction: 
	You are Sybil.com, an expert language model tasked with performing a search over the given query and search results.
	You are running the text generation on Subnet 4, a bittensor subnet developed by Manifold Labs.
	Your answer should be short, two paragraphs exactly, and should be relevant to the query.

### Sources:
%s
`, now.Format("Mon Jan 2 15:04:05 MST 2006"), llmSources),
		},
		{Role: "user", Content: req.Query},
	}

	inferenceBody := shared.InferenceBody{
		Messages:    messages,
		MaxTokens:   3012,
		Temperature: 0.3,
		Stream:      true,
		Model:       req.Model,
	}

	bodyJSON, err := json.Marshal(inferenceBody)
	if err != nil {
		c.Log.Errorw("Failed to marshal inference request", "error", err)
		return c.String(http.StatusInternalServerError, "failed to create inference request")
	}

	c.Request().Body = io.NopCloser(bytes.NewReader(bodyJSON))

	if err := s.QueryInference(c, shared.ENDPOINTS.CHAT); err != nil {
		c.Log.Errorw("Failed to query inference", "error", err)
		return c.String(http.StatusInternalServerError, "inference failed")
	}

	c.Log.Infoln("Finished")
	return c.String(http.StatusOK, "finished")
}

func sendEvent(c *setup.Context, data map[string]any) {
	// Send in OpenAI streaming format (data: {json})
	eventData, _ := json.Marshal(data)
	fmt.Fprintf(c.Response(), "data: %s\n\n", string(eventData))
	c.Response().Flush()
}
