package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"sybil-api/internal/shared"

	"github.com/aidarkhanov/nanoid"
)

// UpdateHistoryInput contains all inputs needed to update an existing chat history
type UpdateHistoryInput struct {
	HistoryID string
	Messages  []shared.ChatMessage
	Settings  *shared.ChatSettings
	UserID    uint64
	Ctx       context.Context
	LogFields map[string]string
}

// UpdateHistoryOutput contains the result of updating a history entry
type UpdateHistoryOutput struct {
	HistoryID string
	UserID    uint64
	Message   string
}

// NewHistoryInput contains all inputs needed to create a new chat history entry with inference
type NewHistoryInput struct {
	Body         []byte
	User         shared.UserMetadata
	RequestID    string
	Ctx          context.Context
	StreamWriter func(token string) error // Optional callback for real-time streaming
}

// NewHistoryOutput contains the results of creating a new history entry and running inference
type NewHistoryOutput struct {
	HistoryID     string
	HistoryIDJSON string // SSE event for history ID
	Stream        bool
	FinalResponse []byte
	ModelName     string
	ModelURL      string
	ModelID       uint64
	InfMetadata   *InferenceMetadata
}

// CompletionRequestNewHistoryLogic initializes a new chat with history for the
// front end
func (im *InferenceHandler) CompletionRequestNewHistoryLogic(input *NewHistoryInput) (*NewHistoryOutput, error) {
	// Parse request body
	var payload shared.InferenceBody
	if err := json.Unmarshal(input.Body, &payload); err != nil {
		return nil, errors.Join(&shared.RequestError{Err: errors.New("failed to parse request body"), StatusCode: 400}, err)
	}

	if len(payload.Messages) == 0 {
		return nil, errors.Join(&shared.RequestError{Err: errors.New("messages cannot be empty"), StatusCode: 400})
	}

	messages := payload.Messages

	// Generate history ID
	// No use checking this; i dont think its even possible for this to fail here
	historyIDNano, _ := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 11)
	historyID := "chat-" + historyIDNano

	// Extract title from first user message
	var title *string
	for _, msg := range messages {
		if msg.Role == "user" && msg.Content != "" {
			titleStr := msg.Content
			if len(titleStr) > 32 {
				titleStr = titleStr[:32]
			}
			title = &titleStr
			break
		}
	}

	var settings shared.ChatSettings
	if err := json.Unmarshal(input.Body, &settings); err != nil {
		return nil, errors.Join(&shared.RequestError{Err: errors.New("failed to parse request body for settings"), StatusCode: 400}, err)
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, errors.Join(shared.ErrInternalServerError, errors.New("failed to marshal settings"), err)
	}

	// Prepare history ID SSE event
	historyIDEvent := map[string]any{
		"type": "history_id",
		"id":   historyID,
	}
	historyIDJSON, _ := json.Marshal(historyIDEvent)

	// Run preprocessing
	reqInfo, preErr := im.Preprocess(input.Ctx, PreprocessInput{
		Body:      input.Body,
		User:      input.User,
		Endpoint:  shared.ENDPOINTS.CHAT,
		RequestID: input.RequestID,
	})

	if preErr != nil {
		// We can safely bubble up pre error since its already a request error
		return nil, errors.Join(preErr, errors.New("failed preprocessing"))
	}

	// Run inference with streaming callback
	out, reqErr := im.DoInference(InferenceInput{
		Req:          reqInfo,
		User:         input.User,
		Ctx:          input.Ctx,
		StreamWriter: input.StreamWriter, // Pass through the streaming callback
	})

	if reqErr != nil {
		return nil, errors.Join(reqErr, errors.New("inference error"))
	}

	if out == nil {
		return &NewHistoryOutput{
			HistoryID:     historyID,
			HistoryIDJSON: string(historyIDJSON),
		}, nil
	}

	// Extract assistant message content from inference output
	var assistantContent string
	if reqInfo.Stream {
		assistantContent = extractContentFromInferenceOutput(out)
	} else {
		assistantContent = extractContentFromFinalResponse(out.FinalResponse)
	}

	if assistantContent == "" {
		return &NewHistoryOutput{
			HistoryID:     historyID,
			HistoryIDJSON: string(historyIDJSON),
			Stream:        reqInfo.Stream,
			FinalResponse: out.FinalResponse,
		}, nil
	}

	// Update history with assistant response asynchronously
	var allMessages []shared.ChatMessage
	allMessages = append(allMessages, messages...)
	allMessages = append(allMessages, shared.ChatMessage{
		Role:    "assistant",
		Content: assistantContent,
	})

	allMessagesJSON, err := json.Marshal(allMessages)
	if err != nil {
		return nil, errors.Join(shared.ErrInternalServerError, errors.New("failed to marshal complete message"), err)
	}

	// insert history into database after response is complete
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
		return nil, errors.Join(shared.ErrInternalServerError, errors.New("failed to insert history into db"), err)
	}

	// update user streak asynchronously
	go func(userID uint64) {
		if err := im.updateUserStreak(userID); err != nil {
			im.Log.Errorw("failed to update user streak", "error", err, "user_id", userID)
		}
	}(input.User.UserID)

	return &NewHistoryOutput{
		HistoryID:     historyID,
		HistoryIDJSON: string(historyIDJSON),
		Stream:        reqInfo.Stream,
		FinalResponse: out.FinalResponse,
		ModelName:     reqInfo.Model,
		ModelID:       reqInfo.ModelMetadata.ModelID,
		ModelURL:      reqInfo.ModelMetadata.URL,
		InfMetadata:   out.Metadata,
	}, nil
}

func (im *InferenceHandler) UpdateHistoryLogic(input *UpdateHistoryInput) (*UpdateHistoryOutput, error) {
	// Check if history exists and get owner user ID
	var ownerUserID uint64
	checkQuery := `SELECT user_id FROM chat_history WHERE history_id = ?`
	err := im.RDB.QueryRowContext(input.Ctx, checkQuery, input.HistoryID).Scan(&ownerUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, &shared.RequestError{StatusCode: 404, Err: errors.New("history not found")}
		}
		return nil, errors.Join(shared.ErrInternalServerError, err)
	}

	// Check authorization
	if ownerUserID != input.UserID {
		return nil, shared.ErrUnauthorized
	}

	// Validate messages
	if len(input.Messages) == 0 {
		return nil, shared.ErrBadRequest
	}

	// Marshal messages
	messagesJSON, err := json.Marshal(input.Messages)
	if err != nil {
		return nil, errors.Join(shared.ErrInternalServerError, err)
	}

	var args []any
	updateQuery := `UPDATE chat_history SET messages = ?, updated_at = NOW()`
	args = append(args, string(messagesJSON))

	if input.Settings != nil {
		settingsJSON, err := json.Marshal(input.Settings)
		if err != nil {
			return nil, errors.Join(shared.ErrInternalServerError, errors.New("failed to marshal settings"), err)
		}
		updateQuery += `, settings = ?`
		args = append(args, string(settingsJSON))
	}

	updateQuery += ` WHERE history_id = ?`
	args = append(args, input.HistoryID)

	_, err = im.WDB.ExecContext(input.Ctx, updateQuery, args...)
	if err != nil {
		return nil, errors.Join(shared.ErrInternalServerError, err)
	}

	// Update user streak asynchronously
	go func(userID uint64) {
		if err := im.updateUserStreak(userID); err != nil {
			im.Log.Errorw("failed to update user streak", "error", err, "user_id", userID)
		}
	}(input.UserID)

	return &UpdateHistoryOutput{
		HistoryID: input.HistoryID,
		UserID:    input.UserID,
		Message:   "History updated successfully",
	}, nil
}

// extractContentFromInferenceOutput extracts assistant content from inference output
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

// extractContentFromFinalResponse extracts assistant content from non-streaming response
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
