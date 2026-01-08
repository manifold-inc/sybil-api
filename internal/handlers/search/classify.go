package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"sybil-api/internal/ctx"

	"github.com/labstack/echo/v4"
)

const (
	EmbeddingsAPIURL = "https://api.sybil.com/v1/embeddings"
	EmbeddingsModel  = "distilbert/distilbert-base-uncased"
)

var searchReferenceTexts = []string{
	"what is the current weather today",
	"latest news and recent events",
	"current stock price and market data",
	"what time is it now",
	"recent updates and breaking news",
	"sports scores and game results today",
	"flight status and arrival time",
	"where can I buy this product near me",
	"restaurant hours and reviews",
	"upcoming events and concert dates",
}

var noSearchReferenceTexts = []string{
	"explain a concept or definition",
	"write code and help with programming",
	"creative writing and brainstorming",
	"how to do something tutorial",
	"translate text between languages",
	"summarize this text for me",
	"solve this math equation",
	"debug and fix this error",
	"generate ideas for a project",
	"reformat this data as json",
}

type classifyRequestBody struct {
	Query string `json:"query"`
}

type ClassifyResponse struct {
	NeedsSearch bool   `json:"needs_search"`
	Query       string `json:"query"`
}

func (s *SearchManager) Classify(cc echo.Context) error {
	c := cc.(*ctx.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.LogValues.AddError(err)
		return c.String(http.StatusBadRequest, "failed to read request body")
	}

	var req classifyRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		c.LogValues.AddError(err)
		return c.String(http.StatusBadRequest, "invalid JSON format")
	}

	if req.Query == "" {
		c.LogValues.AddError(fmt.Errorf("query is required"))
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "query is required"})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()

	result := classifyQuery(ctx, c, req.Query, c.User.APIKey)

	response := ClassifyResponse{
		NeedsSearch: result.NeedsSearch,
		Query:       req.Query,
	}

	return c.JSON(http.StatusOK, response)
}

type classifyResult struct {
	NeedsSearch bool
}

func classifyQuery(ctx context.Context, c *ctx.Context, query string, apiKey string) *classifyResult {
	if result := classifyWithHeuristics(query); result != nil {
		return result
	}
	return classifyWithEmbeddings(ctx, c, query, apiKey)
}

func classifyWithHeuristics(query string) *classifyResult {
	q := strings.ToLower(strings.TrimSpace(query))

	searchTriggers := []string{
		"weather today", "weather in", "weather for",
		"stock price", "bitcoin price", "crypto price",
		"latest news", "breaking news", "recent news",
		"current score", "game score",
		"what time is it", "current time", "price of",
	}
	for _, trigger := range searchTriggers {
		if strings.Contains(q, trigger) {
			return &classifyResult{
				NeedsSearch: true,
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
			return &classifyResult{
				NeedsSearch: false,
			}
		}
	}

	return nil
}

type embeddingsRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func classifyWithEmbeddings(ctx context.Context, c *ctx.Context, query string, apiKey string) *classifyResult {
	queryEmbedding, err := getEmbedding(ctx, c, query, apiKey)
	if err != nil {
		c.LogValues.AddError(errors.Join(errors.New("failed to get query embedding, defaulting to no search"), err))
		return &classifyResult{
			NeedsSearch: false,
		}
	}

	searchEmbeddings, err := getEmbeddings(ctx, c, searchReferenceTexts, apiKey)
	if err != nil {
		c.LogValues.AddError(errors.Join(errors.New("failed to get search reference embeddings"), err))
		return &classifyResult{
			NeedsSearch: false,
		}
	}

	noSearchEmbeddings, err := getEmbeddings(ctx, c, noSearchReferenceTexts, apiKey)
	if err != nil {
		c.LogValues.AddError(errors.Join(errors.New("failed to get no-search reference embeddings"), err))
		return &classifyResult{
			NeedsSearch: false,
		}
	}

	searchSimilarity := averageCosineSimilarity(queryEmbedding, searchEmbeddings)
	noSearchSimilarity := averageCosineSimilarity(queryEmbedding, noSearchEmbeddings)
	diff := math.Abs(searchSimilarity - noSearchSimilarity)
	needsSearch := searchSimilarity > noSearchSimilarity && diff > 0.015

	return &classifyResult{
		NeedsSearch: needsSearch,
	}
}

func getEmbedding(ctx context.Context, c *ctx.Context, text string, apiKey string) ([]float64, error) {
	embeddings, err := getEmbeddings(ctx, c, []string{text}, apiKey)
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, io.EOF
	}
	return embeddings[0], nil
}

func getEmbeddings(ctx context.Context, c *ctx.Context, texts []string, apiKey string) ([][]float64, error) {
	reqBody := embeddingsRequest{
		Model: EmbeddingsModel,
		Input: texts,
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", EmbeddingsAPIURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.LogValues.AddError(fmt.Errorf("embeddings API error: status=%d body=%s", resp.StatusCode, string(body)))
		return nil, io.EOF
	}

	var embResp embeddingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, err
	}

	result := make([][]float64, len(embResp.Data))
	for _, d := range embResp.Data {
		result[d.Index] = d.Embedding
	}

	return result, nil
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

func averageCosineSimilarity(query []float64, references [][]float64) float64 {
	if len(references) == 0 {
		return 0
	}

	var total float64
	for _, ref := range references {
		total += cosineSimilarity(query, ref)
	}

	return total / float64(len(references))
}
