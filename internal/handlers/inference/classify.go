package inference

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"sybil-api/internal/shared"
)

type ClassificationResult struct {
	NeedsSearch bool   `json:"needs_search"`
	Reason      string `json:"reason,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

type ClassifyInput struct {
	Query     string
	UserID    uint64
	Model     string
	LogFields map[string]string
}

const DefaultClassifierModel = "Qwen/Qwen3-Coder-480B-A35B-Instruct-FP8"

const classifierSystemPrompt = `You are a query classifier. Determine if the user's query requires real-time web search to answer accurately.

Respond with JSON only: {"needs_search": true/false, "reason": "brief reason"}

NEEDS SEARCH (true):
- Current events, breaking news, recent developments
- Real-time data: weather, stock prices, sports scores, exchange rates
- Questions containing: "today", "latest", "current", "recent", "now", "this week"
- Information that changes frequently (company news, product releases, prices)
- Fact-checking recent claims or statements
- Finding specific websites, articles, or current contact information

NO SEARCH NEEDED (false):
- General knowledge, definitions, scientific concepts
- Historical facts and events
- Math calculations, coding help, debugging
- Creative writing, brainstorming, opinions
- How-to explanations, tutorials, advice
- Philosophical questions, hypotheticals
- Language translation, grammar help

Examples:
- "What's the weather in NYC?" → {"needs_search": true, "reason": "real-time weather data"}
- "Explain quantum entanglement" → {"needs_search": false, "reason": "general knowledge"}
- "Latest news on AI" → {"needs_search": true, "reason": "current events"}
- "Write a Python sort function" → {"needs_search": false, "reason": "coding help"}`

func (im *InferenceHandler) ClassifyNeedsSearch(ctx context.Context, input ClassifyInput) (*ClassificationResult, error) {
	if result := classifyWithHeuristics(input.Query); result != nil {
		return result, nil
	}

	return im.classifyWithLLM(ctx, input)
}

func classifyWithHeuristics(query string) *ClassificationResult {
	q := strings.ToLower(strings.TrimSpace(query))

	searchTriggers := []string{
		"weather today", "weather in", "weather for",
		"stock price", "bitcoin price", "crypto price",
		"latest news", "breaking news", "recent news",
		"current score", "game score",
		"what time is it", "current time",
	}
	for _, trigger := range searchTriggers {
		if strings.Contains(q, trigger) {
			return &ClassificationResult{
				NeedsSearch: true,
				Reason:      "matched search trigger pattern",
				Confidence:  "high",
			}
		}
	}

	noSearchPrefixes := []string{
		"explain ", "define ", "what is the definition",
		"write a ", "write me ", "create a ",
		"how do i ", "how to ", "how can i ",
		"translate ", "convert ",
		"calculate ", "compute ", "solve ",
		"debug ", "fix this ", "refactor ",
	}
	for _, prefix := range noSearchPrefixes {
		if strings.HasPrefix(q, prefix) {
			return &ClassificationResult{
				NeedsSearch: false,
				Reason:      "matched no-search prefix pattern",
				Confidence:  "high",
			}
		}
	}

	return nil
}

func (im *InferenceHandler) classifyWithLLM(ctx context.Context, input ClassifyInput) (*ClassificationResult, error) {
	model := input.Model
	if model == "" {
		model = DefaultClassifierModel
	}

	messages := []shared.ChatMessage{
		{Role: "system", Content: classifierSystemPrompt},
		{Role: "user", Content: input.Query},
	}

	bodyJSON, err := json.Marshal(shared.InferenceBody{
		Messages:    messages,
		MaxTokens:   100, // Classification needs very few tokens
		Temperature: 0,   // Deterministic output
		Stream:      false,
		Model:       model,
	})
	if err != nil {
		return nil, err
	}

	reqInfo := &shared.RequestInfo{
		Body:      bodyJSON,
		UserID:    input.UserID,
		ID:        "classify-" + time.Now().Format("20060102150405"),
		StartTime: time.Now(),
		Endpoint:  shared.ENDPOINTS.CHAT,
		Model:     model,
		Stream:    false,
	}

	out, reqErr := im.DoInference(InferenceInput{
		Req:       reqInfo,
		User:      shared.UserMetadata{UserID: input.UserID},
		Ctx:       ctx,
		LogFields: input.LogFields,
	})

	if reqErr != nil {
		im.Log.Warnw("Classification failed, defaulting to no search",
			"error", reqErr.Error(),
			"query", input.Query)
		return &ClassificationResult{
			NeedsSearch: false,
			Reason:      "classification failed, defaulting to no search",
			Confidence:  "low",
		}, nil
	}

	if out == nil || len(out.FinalResponse) == 0 {
		return &ClassificationResult{
			NeedsSearch: false,
			Reason:      "empty response, defaulting to no search",
			Confidence:  "low",
		}, nil
	}

	return parseClassificationResponse(out.FinalResponse)
}

func parseClassificationResponse(response []byte) (*ClassificationResult, error) {
	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(response, &openAIResp); err != nil {
		return nil, errors.New("failed to parse LLM response")
	}

	if len(openAIResp.Choices) == 0 {
		return &ClassificationResult{
			NeedsSearch: false,
			Reason:      "no choices in response, defaulting to no search",
			Confidence:  "low",
		}, nil
	}

	content := strings.TrimSpace(openAIResp.Choices[0].Message.Content)

	content = extractJSON(content)

	var result ClassificationResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		lower := strings.ToLower(content)
		if strings.Contains(lower, `"needs_search": true`) || strings.Contains(lower, `"needs_search":true`) {
			return &ClassificationResult{
				NeedsSearch: true,
				Reason:      "parsed from response",
				Confidence:  "medium",
			}, nil
		}
		return &ClassificationResult{
			NeedsSearch: false,
			Reason:      "parsed from response",
			Confidence:  "medium",
		}, nil
	}

	result.Confidence = "high"
	return &result, nil
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
	}

	return strings.TrimSpace(s)
}
