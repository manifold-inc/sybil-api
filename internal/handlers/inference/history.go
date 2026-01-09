package inference

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sybil-api/internal/shared"

	"github.com/aidarkhanov/nanoid"
	"go.uber.org/zap"
	"google.golang.org/api/customsearch/v1"
)

const (
	EmbeddingsAPIURL           = "https://api.sybil.com/v1/embeddings"
	EmbeddingsModel            = "distilbert/distilbert-base-uncased"
	NumSearchResults           = 5
	SearchSensitivityThreshold = 0.025
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
	"hello",
}

type ChatInput struct {
	ChatID       string
	Messages     []shared.ChatMessage
	Settings     *shared.ChatSettings
	User         shared.UserMetadata
	RequestID    string
	Ctx          context.Context
	StreamWriter func(string) error
}

type ChatOutput struct {
	HistoryID     string
	IsNew         bool
	Stream        bool
	FinalResponse []byte
	ModelName     string
	ModelURL      string
	ModelID       uint64
	InfMetadata   *InferenceMetadata
	SearchUsed    bool
	Sources       []shared.SearchResults
}

func (im *InferenceHandler) Chat(input *ChatInput) (*ChatOutput, error) {
	if len(input.Messages) == 0 {
		return nil, &shared.RequestError{Err: errors.New("messages cannot be empty"), StatusCode: 400}
	}

	search := ""
	if input.Settings != nil {
		search = input.Settings.Search
	}

	var lastUserMessage string
	for i := len(input.Messages) - 1; i >= 0; i-- {
		if input.Messages[i].Role == "user" {
			lastUserMessage = input.Messages[i].Content
			break
		}
	}

	var searchUsed bool
	var searchSources []shared.SearchResults
	messages := input.Messages

	sendStatus := func(status string, sources []shared.SearchResults) {
		if input.StreamWriter != nil {
			statusEvent := map[string]any{"type": "status", "status": status}
			if len(sources) > 0 {
				statusEvent["sources"] = sources
			}
			statusJSON, _ := json.Marshal(statusEvent)
			_ = input.StreamWriter(fmt.Sprintf("data: %s", statusJSON))
		}
	}

	if search == "on" || (search == "auto" && lastUserMessage != "") {
		needsSearch := search == "on"

		if search == "auto" && im.SearchConfig != nil && im.SearchConfig.ClassifyQuery != nil {
			classifyCtx, cancel := context.WithTimeout(input.Ctx, 10*time.Second)
			defer cancel()

			needsSearch = im.SearchConfig.ClassifyQuery(classifyCtx, lastUserMessage, input.User.APIKey)
		}

		if needsSearch && im.SearchConfig != nil && im.SearchConfig.DoSearch != nil && lastUserMessage != "" {
			sendStatus("searching", nil)

			searchResults, err := im.SearchConfig.DoSearch(lastUserMessage)
			if err != nil {
				im.Log.Warnw("search failed, continuing without search context", "error", err)
			} else if searchResults != nil && len(searchResults.Results) > 0 {
				searchUsed = true
				searchSources = searchResults.Results

				if input.StreamWriter != nil {
					sourcesEvent := map[string]any{"type": "sources", "sources": searchSources}
					sourcesJSON, _ := json.Marshal(sourcesEvent)
					_ = input.StreamWriter(fmt.Sprintf("data: %s", sourcesJSON))
				}

				searchContext := formatSearchContext(searchResults.Results)
				if searchContext != "" {
					searchSystemMsg := shared.ChatMessage{
						Role:    "system",
						Content: fmt.Sprintf("\n\n### Web Search Results:\n%s\n\nUse the above search results to answer the question. Cite sources using numbered references like [1], [2], [3] inline in your response. Do not use markdown links. You can use real-time data from the search results to answer the question.", searchContext),
					}

					messages = append([]shared.ChatMessage{searchSystemMsg}, input.Messages...)
				}
			}
		}
	}

	isNew := input.ChatID == ""
	historyID := input.ChatID

	if isNew {
		historyIDNano, _ := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 11)
		historyID = "chat-" + historyIDNano
	} else {
		var ownerUserID uint64
		checkQuery := `SELECT user_id FROM chat_history WHERE history_id = ?`
		err := im.RDB.QueryRowContext(input.Ctx, checkQuery, historyID).Scan(&ownerUserID)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, &shared.RequestError{StatusCode: 404, Err: errors.New("history not found")}
			}
			return nil, errors.Join(shared.ErrInternalServerError, err)
		}
		if ownerUserID != input.User.UserID {
			return nil, shared.ErrUnauthorized
		}
	}

	inferenceBody := shared.InferenceBody{
		Messages: messages,
		Stream:   true,
	}
	if input.Settings != nil {
		inferenceBody.Model = input.Settings.Model
		inferenceBody.Temperature = input.Settings.Temperature
		inferenceBody.MaxTokens = input.Settings.MaxTokens
		inferenceBody.Stream = input.Settings.Stream
		inferenceBody.Logprobs = input.Settings.Logprobs
	}

	bodyBytes, err := json.Marshal(inferenceBody)
	if err != nil {
		return nil, errors.Join(shared.ErrInternalServerError, errors.New("failed to marshal inference body"), err)
	}

	reqInfo, preErr := im.Preprocess(input.Ctx, PreprocessInput{
		Body:      bodyBytes,
		User:      input.User,
		Endpoint:  shared.ENDPOINTS.CHAT,
		RequestID: input.RequestID,
	})
	if preErr != nil {
		return nil, errors.Join(preErr, errors.New("failed preprocessing"))
	}

	sendStatus("generating", nil)

	out, reqErr := im.DoInference(InferenceInput{
		Req:          reqInfo,
		User:         input.User,
		Ctx:          input.Ctx,
		StreamWriter: input.StreamWriter,
	})
	if reqErr != nil {
		return nil, errors.Join(reqErr, errors.New("inference error"))
	}

	if out == nil {
		return &ChatOutput{
			HistoryID:  historyID,
			IsNew:      isNew,
			SearchUsed: searchUsed,
			Sources:    searchSources,
		}, nil
	}

	var assistantContent string
	if reqInfo.Stream {
		assistantContent = extractContentFromInferenceOutput(out)
	} else {
		assistantContent = extractContentFromFinalResponse(out.FinalResponse)
	}

	var allMessages []shared.ChatMessage
	allMessages = append(allMessages, input.Messages...)
	if assistantContent != "" {
		assistantMsg := shared.ChatMessage{
			Role:    "assistant",
			Content: assistantContent,
		}
		if searchUsed && len(searchSources) > 0 {
			assistantMsg.Sources = convertSearchResultsToMessageSources(searchSources)
		}
		allMessages = append(allMessages, assistantMsg)
	}

	allMessagesJSON, err := json.Marshal(allMessages)
	if err != nil {
		return nil, errors.Join(shared.ErrInternalServerError, errors.New("failed to marshal messages"), err)
	}

	if isNew {
		var title *string
		for _, msg := range input.Messages {
			if msg.Role == "user" && msg.Content != "" {
				titleStr := msg.Content
				if len(titleStr) > 32 {
					titleStr = titleStr[:32]
				}
				title = &titleStr
				break
			}
		}

		var settingsJSON []byte
		if input.Settings != nil {
			settingsJSON, err = json.Marshal(input.Settings)
			if err != nil {
				return nil, errors.Join(shared.ErrInternalServerError, errors.New("failed to marshal settings"), err)
			}
		}

		insertQuery := `
			INSERT INTO chat_history (
				user_id,
				history_id,
				messages,
				title,
				icon,
				settings
			) VALUES (?, ?, ?, ?, ?, ?)
		`

		_, err = im.WDB.Exec(insertQuery,
			input.User.UserID,
			historyID,
			string(allMessagesJSON),
			title,
			nil, // icon
			string(settingsJSON),
		)
		if err != nil {
			return nil, errors.Join(shared.ErrInternalServerError, errors.New("failed to insert history"), err)
		}
	} else {
		var args []any
		updateQuery := `UPDATE chat_history SET messages = ?, updated_at = NOW()`
		args = append(args, string(allMessagesJSON))

		if input.Settings != nil {
			settingsJSON, err := json.Marshal(input.Settings)
			if err != nil {
				return nil, errors.Join(shared.ErrInternalServerError, errors.New("failed to marshal settings"), err)
			}
			updateQuery += `, settings = ?`
			args = append(args, string(settingsJSON))
		}

		updateQuery += ` WHERE history_id = ?`
		args = append(args, historyID)

		_, err = im.WDB.ExecContext(input.Ctx, updateQuery, args...)
		if err != nil {
			return nil, errors.Join(shared.ErrInternalServerError, err)
		}
	}

	go func(userID uint64) {
		if err := im.updateUserStreak(userID); err != nil {
			im.Log.Errorw("failed to update user streak", "error", err, "user_id", userID)
		}
	}(input.User.UserID)

	return &ChatOutput{
		HistoryID:     historyID,
		IsNew:         isNew,
		Stream:        reqInfo.Stream,
		FinalResponse: out.FinalResponse,
		ModelName:     reqInfo.Model,
		ModelURL:      reqInfo.ModelMetadata.URL,
		ModelID:       reqInfo.ModelMetadata.ModelID,
		InfMetadata:   out.Metadata,
		SearchUsed:    searchUsed,
		Sources:       searchSources,
	}, nil
}

func convertSearchResultsToMessageSources(results []shared.SearchResults) []shared.MessageSource {
	sources := make([]shared.MessageSource, 0, len(results))
	for _, r := range results {
		source := shared.MessageSource{}
		if r.Title != nil {
			source.Title = *r.Title
		}
		if r.URL != nil {
			source.URL = *r.URL
		}
		if r.Content != nil {
			source.Content = *r.Content
		}
		if r.Thumbnail != nil {
			source.Thumbnail = *r.Thumbnail
		}
		if r.Website != nil {
			source.Website = *r.Website
		}
		sources = append(sources, source)
	}
	return sources
}

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

func extractContentFromInferenceOutput(out *InferenceOutput) string {
	if out == nil || len(out.FinalResponse) == 0 {
		return ""
	}

	// FinalResponse contains the marshaled array of response chunks
	var chunks []json.RawMessage
	if err := json.Unmarshal(out.FinalResponse, &chunks); err != nil {
		return ""
	}

	var fullContent strings.Builder
	for _, chunkData := range chunks {
		var chunk shared.Response
		if err := json.Unmarshal(chunkData, &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		if choice.Delta == nil {
			continue
		}

		if choice.Delta.Content != "" {
			fullContent.WriteString(choice.Delta.Content)
		}
	}

	return fullContent.String()
}

func extractContentFromFinalResponse(finalResponse []byte) string {
	if len(finalResponse) == 0 {
		return ""
	}

	var response shared.Response
	if err := json.Unmarshal(finalResponse, &response); err != nil {
		return ""
	}

	if len(response.Choices) == 0 {
		return ""
	}

	choice := response.Choices[0]
	if choice.Message != nil {
		return choice.Message.Content
	}

	return ""
}

func (im *InferenceHandler) updateUserStreak(userID uint64) error {
	var lastChatStr sql.NullString
	var currentStreak uint64

	err := im.RDB.QueryRow(`
		SELECT last_chat, streak 
		FROM user 
		WHERE id = ?
	`, userID).Scan(&lastChatStr, &currentStreak)
	if err != nil {
		return fmt.Errorf("failed to get user streak data: %w", err)
	}

	var lastChat sql.NullTime
	if lastChatStr.Valid && lastChatStr.String != "" {
		formats := []string{
			"2006-01-02 15:04:05",
			time.RFC3339,
			"2006-01-02T15:04:05Z07:00",
			"2006-01-02 15:04:05.000000",
		}

		var parsedTime time.Time
		var parseErr error
		for _, format := range formats {
			parsedTime, parseErr = time.Parse(format, lastChatStr.String)
			if parseErr == nil {
				lastChat = sql.NullTime{Time: parsedTime, Valid: true}
				break
			}
		}

		if parseErr != nil {
			im.Log.Warnw("Failed to parse last_chat timestamp", "error", parseErr, "value", lastChatStr.String)
		}
	}

	now := time.Now()
	todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var newStreak uint64
	updateStreak := false

	if lastChat.Valid {
		lastChatDate := lastChat.Time
		lastChatMidnight := time.Date(lastChatDate.Year(), lastChatDate.Month(), lastChatDate.Day(), 0, 0, 0, 0, lastChatDate.Location())

		if !todayMidnight.Equal(lastChatMidnight) {
			updateStreak = true
			expectedDate := lastChatMidnight.AddDate(0, 0, 1)
			if todayMidnight.Equal(expectedDate) {
				newStreak = currentStreak + 1
			} else {
				newStreak = 1
			}
		} else {
			newStreak = currentStreak
		}
	} else {
		updateStreak = true
		newStreak = 1
	}

	if updateStreak {
		_, err = im.WDB.Exec(`
			UPDATE user 
			SET streak = ?, last_chat = ? 
			WHERE id = ?
		`, newStreak, now, userID)
		if err != nil {
			return fmt.Errorf("failed to update user streak: %w", err)
		}
	} else {
		_, err = im.WDB.Exec(`
			UPDATE user 
			SET last_chat = ? 
			WHERE id = ?
		`, now, userID)
		if err != nil {
			return fmt.Errorf("failed to update last_chat: %w", err)
		}
	}

	return nil
}

func ClassifyQuery(ctx context.Context, query string, apiKey string) bool {
	if result := classifyWithHeuristics(query); result != nil {
		return result.needsSearch
	}
	return classifyWithEmbeddings(ctx, query, apiKey)
}

type classifyResult struct {
	needsSearch bool
}

func classifyWithHeuristics(query string) *classifyResult {
	q := strings.ToLower(strings.TrimSpace(query))

	searchTriggers := []string{
		"weather today", "weather in", "weather for",
		"stock price", "bitcoin price", "crypto price",
		"latest news", "breaking news", "recent news",
		"current score", "game score", "search",
		"what time is it", "current time", "price of",
	}
	for _, trigger := range searchTriggers {
		if strings.Contains(q, trigger) {
			return &classifyResult{needsSearch: true}
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
			return &classifyResult{needsSearch: false}
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

func classifyWithEmbeddings(ctx context.Context, query string, apiKey string) bool {
	queryEmbedding, err := getEmbedding(ctx, query, apiKey)
	if err != nil {
		return false
	}

	searchEmbeddings, err := getEmbeddings(ctx, searchReferenceTexts, apiKey)
	if err != nil {
		return false
	}

	noSearchEmbeddings, err := getEmbeddings(ctx, noSearchReferenceTexts, apiKey)
	if err != nil {
		return false
	}

	searchSimilarity := averageCosineSimilarity(queryEmbedding, searchEmbeddings)
	noSearchSimilarity := averageCosineSimilarity(queryEmbedding, noSearchEmbeddings)
	diff := math.Abs(searchSimilarity - noSearchSimilarity)
	return searchSimilarity > noSearchSimilarity && diff > SearchSensitivityThreshold
}

func getEmbedding(ctx context.Context, text string, apiKey string) ([]float64, error) {
	embeddings, err := getEmbeddings(ctx, []string{text}, apiKey)
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, io.EOF
	}
	return embeddings[0], nil
}

func getEmbeddings(ctx context.Context, texts []string, apiKey string) ([][]float64, error) {
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
		return nil, fmt.Errorf("embeddings API error: status=%d body=%s", resp.StatusCode, string(body))
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

func QueryGoogleSearch(googleService *customsearch.Service, log *zap.SugaredLogger, googleSearchEngineID string, query string) (*shared.SearchResponseBody, error) {
	search := googleService.Cse.List().Q(query).Cx(googleSearchEngineID).Num(NumSearchResults)

	res, err := search.Do()
	if err != nil {
		return nil, err
	}

	results := make([]shared.SearchResults, len(res.Items))
	for i, item := range res.Items {
		title := item.Title
		content := item.Snippet
		link := item.Link
		source := ""
		website := ""
		metadata := ""
		publishedDate := ""

		if item.Pagemap != nil {
			var pagemap map[string]any
			if err := json.Unmarshal(item.Pagemap, &pagemap); err != nil {
				log.Errorw("failed to unmarshal pagemap", "error", err.Error())
				continue
			}

			if metatags, ok := pagemap["metatags"].([]any); ok {
				if metatag := shared.GetFirstMap(metatags); metatag != nil {
					publishedDate = shared.GetString(metatag, "article:published_time")
					if desc := shared.GetString(metatag, "og:description"); desc != "" {
						metadata = desc
					}
					if siteName := shared.GetString(metatag, "og:site_name"); siteName != "" {
						website = siteName
					}
				}
			}
		}

		if link != "" {
			if parsed, err := url.Parse(link); err == nil {
				source = parsed.Hostname()
				if website == "" {
					website = source
				}
				parsedURL := strings.Split(source, ".")
				results[i] = shared.SearchResults{
					Title:         &title,
					Content:       &content,
					URL:           &link,
					ParsedURL:     &parsedURL,
					Source:        &source,
					Website:       &website,
					Metadata:      &metadata,
					PublishedDate: &publishedDate,
				}
				continue
			}
		}

		emptyParsedURL := []string{}
		results[i] = shared.SearchResults{
			Title:         &title,
			Content:       &content,
			URL:           &link,
			ParsedURL:     &emptyParsedURL,
			Source:        &source,
			Website:       &website,
			Metadata:      &metadata,
			PublishedDate: &publishedDate,
		}
	}

	totalResults, err := strconv.Atoi(res.SearchInformation.TotalResults)
	if err != nil {
		log.Warnw("error converting total results to int", "error", err.Error())
		totalResults = 0
	}

	return &shared.SearchResponseBody{
		Query:           query,
		NumberOfResults: totalResults,
		Results:         results,
	}, nil
}
