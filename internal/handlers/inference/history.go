package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sybil-api/internal/shared"
	"time"

	"github.com/aidarkhanov/nanoid"
	"go.uber.org/zap"
)

// NewHistoryInput contains all inputs needed to create a new chat history entry with inference
type NewHistoryInput struct {
	Body         []byte
	User         shared.UserMetadata
	RequestID    string
	Ctx          context.Context
	LogFields    map[string]string
	StreamWriter func(token string) error // Optional callback for real-time streaming
}

// NewHistoryOutput contains the results of creating a new history entry and running inference
type NewHistoryOutput struct {
	HistoryID     string
	HistoryIDJSON string // SSE event for history ID
	Stream        bool
	FinalResponse []byte
	Error         *HistoryError
}

// UpdateHistoryInput contains all inputs needed to update an existing chat history
type UpdateHistoryInput struct {
	HistoryID string
	Messages  []shared.ChatMessage
	UserID    uint64
	Ctx       context.Context
	LogFields map[string]string
}

// UpdateHistoryOutput contains the result of updating a history entry
type UpdateHistoryOutput struct {
	HistoryID string
	UserID    uint64
	Message   string
	Error     *HistoryError
}

// HistoryError represents a structured error for history operations
type HistoryError struct {
	StatusCode int
	Message    string
	Err        error
}

func (im *InferenceHandler) CompletionRequestNewHistoryLogic(input *NewHistoryInput) (*NewHistoryOutput, error) {
	log := logWithFields(im.Log, input.LogFields)

	// Parse request body
	var payload shared.InferenceBody
	if err := json.Unmarshal(input.Body, &payload); err != nil {
		log.Errorw("Failed to parse request body", "error", err.Error())
		return &NewHistoryOutput{
			Error: &HistoryError{
				StatusCode: 400,
				Message:    "invalid JSON format",
				Err:        err,
			},
		}, nil
	}

	if len(payload.Messages) == 0 {
		return &NewHistoryOutput{
			Error: &HistoryError{
				StatusCode: 400,
				Message:    "messages are required",
				Err:        errors.New("messages are required"),
			},
		}, nil
	}

	messages := payload.Messages

	// Generate history ID
	historyIDNano, err := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 11)
	if err != nil {
		log.Errorw("Failed to generate history nanoid", "error", err)
		return &NewHistoryOutput{
			Error: &HistoryError{
				StatusCode: 500,
				Message:    "failed to generate history ID",
				Err:        err,
			},
		}, nil
	}
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

	// Marshal messages for DB insert
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		log.Errorw("Failed to marshal initial messages", "error", err)
		return &NewHistoryOutput{
			Error: &HistoryError{
				StatusCode: 500,
				Message:    "failed to prepare history",
				Err:        err,
			},
		}, nil
	}

	settings := map[string]any{}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		log.Errorw("Failed to marshal initial settings", "error", err)
		return &NewHistoryOutput{
			Error: &HistoryError{
				StatusCode: 500,
				Message:    "failed to prepare history",
				Err:        err,
			},
		}, nil
	}

	// Insert into database
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
		string(messagesJSON),
		title,
		nil, // icon
		string(settingsJSON),
	)
	if err != nil {
		log.Errorw("Failed to insert history into database", "error", err)
		return &NewHistoryOutput{
			Error: &HistoryError{
				StatusCode: 500,
				Message:    "failed to create history",
				Err:        err,
			},
		}, nil
	}

	log.Infow("Chat history created", "history_id", historyID, "user_id", input.User.UserID)

	// Prepare history ID SSE event
	historyIDEvent := map[string]any{
		"type": "history_id",
		"id":   historyID,
	}
	historyIDJSON, _ := json.Marshal(historyIDEvent)

	// Build logfields for inference
	inferenceLogFields := map[string]string{}
	if input.LogFields != nil {
		maps.Copy(inferenceLogFields, input.LogFields)
	}
	inferenceLogFields["history_id"] = historyID

	// Run preprocessing
	reqInfo, preErr := im.Preprocess(PreprocessInput{
		Body:      input.Body,
		User:      input.User,
		Endpoint:  shared.ENDPOINTS.CHAT,
		RequestID: input.RequestID,
		LogFields: inferenceLogFields,
	})

	if preErr != nil {
		log.Warnw("Preprocessing failed", "error", preErr.Err)
		return &NewHistoryOutput{
			HistoryID:     historyID,
			HistoryIDJSON: string(historyIDJSON),
			Error: &HistoryError{
				StatusCode: preErr.StatusCode,
				Message:    "inference error",
				Err:        preErr.Err,
			},
		}, nil
	}

	// Run inference with streaming callback
	out, reqErr := im.DoInference(InferenceInput{
		Req:          reqInfo,
		User:         input.User,
		Ctx:          input.Ctx,
		LogFields:    inferenceLogFields,
		StreamWriter: input.StreamWriter, // Pass through the streaming callback
	})

	if reqErr != nil {
		if reqErr.StatusCode >= 500 && reqErr.Err != nil {
			log.Warnw("Inference error", "error", reqErr.Err.Error())
		}
		return &NewHistoryOutput{
			HistoryID:     historyID,
			HistoryIDJSON: string(historyIDJSON),
			Error: &HistoryError{
				StatusCode: reqErr.StatusCode,
				Message:    "inference error",
				Err:        reqErr.Err,
			},
		}, nil
	}

	if out == nil {
		return &NewHistoryOutput{
			HistoryID:     historyID,
			HistoryIDJSON: string(historyIDJSON),
		}, nil
	}

	// Extract assistant message content from inference output
	var assistantContent string
	if out.Stream {
		assistantContent = extractContentFromInferenceOutput(out)
	} else {
		assistantContent = extractContentFromFinalResponse(out.FinalResponse)
	}

	// Update history with assistant response asynchronously
	if assistantContent != "" {
		var allMessages []shared.ChatMessage
		allMessages = append(allMessages, messages...)
		allMessages = append(allMessages, shared.ChatMessage{
			Role:    "assistant",
			Content: assistantContent,
		})

		allMessagesJSON, err := json.Marshal(allMessages)
		if err != nil {
			log.Errorw("Failed to marshal complete messages", "error", err)
		} else {
			go func(userID uint64, historyID string, messagesJSON []byte, log *zap.SugaredLogger) {
				updateQuery := `
			UPDATE chat_history 
			SET messages = ?, updated_at = NOW()
			WHERE history_id = ?
		`

				_, err := im.WDB.Exec(updateQuery, string(messagesJSON), historyID)
				if err != nil {
					log.Errorw("Failed to update history in database", "error", err, "history_id", historyID)
					return
				}

				log.Infow("Chat history updated with assistant response", "history_id", historyID, "user_id", userID)

				if err := im.updateUserStreak(userID, log); err != nil {
					log.Errorw("Failed to update user streak", "error", err, "user_id", userID)
				}
			}(input.User.UserID, historyID, allMessagesJSON, log)
		}
	}

	return &NewHistoryOutput{
		HistoryID:     historyID,
		HistoryIDJSON: string(historyIDJSON),
		Stream:        out.Stream,
		FinalResponse: out.FinalResponse,
	}, nil
}

func (im *InferenceHandler) UpdateHistoryLogic(input *UpdateHistoryInput) (*UpdateHistoryOutput, error) {
	log := logWithFields(im.Log, input.LogFields)

	// Check if history exists and get owner user ID
	var ownerUserID uint64
	checkQuery := `SELECT user_id FROM chat_history WHERE history_id = ?`
	err := im.RDB.QueryRowContext(input.Ctx, checkQuery, input.HistoryID).Scan(&ownerUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Errorw("History not found", "error", err.Error(), "history_id", input.HistoryID)
			return &UpdateHistoryOutput{
				Error: &HistoryError{
					StatusCode: 404,
					Message:    "history not found",
					Err:        err,
				},
			}, nil
		}
		log.Errorw("Failed to check history", "error", err.Error(), "history_id", input.HistoryID)
		return &UpdateHistoryOutput{
			Error: &HistoryError{
				StatusCode: 500,
				Message:    "internal server error",
				Err:        err,
			},
		}, nil
	}

	// Check authorization
	if ownerUserID != input.UserID {
		log.Errorw("Unauthorized access to history", "history_id", input.HistoryID, "user_id", input.UserID, "owner_id", ownerUserID)
		return &UpdateHistoryOutput{
			Error: &HistoryError{
				StatusCode: 403,
				Message:    "unauthorized",
				Err:        errors.New("unauthorized access"),
			},
		}, nil
	}

	log.Infow("Updating chat history",
		"history_id", input.HistoryID,
		"user_id", input.UserID)

	// Validate messages
	if len(input.Messages) == 0 {
		return &UpdateHistoryOutput{
			Error: &HistoryError{
				StatusCode: 400,
				Message:    "messages cannot be empty",
				Err:        errors.New("messages cannot be empty"),
			},
		}, nil
	}

	// Marshal messages
	messagesJSON, err := json.Marshal(input.Messages)
	if err != nil {
		log.Errorw("Failed to marshal messages", "error", err)
		return &UpdateHistoryOutput{
			Error: &HistoryError{
				StatusCode: 500,
				Message:    "internal server error",
				Err:        err,
			},
		}, nil
	}

	// Update database
	updateQuery := `
		UPDATE chat_history 
		SET messages = ?, updated_at = NOW()
		WHERE history_id = ?
	`

	_, err = im.WDB.ExecContext(input.Ctx, updateQuery, string(messagesJSON), input.HistoryID)
	if err != nil {
		log.Errorw("Failed to update history in database",
			"error", err.Error(),
			"history_id", input.HistoryID)
		return &UpdateHistoryOutput{
			Error: &HistoryError{
				StatusCode: 500,
				Message:    "internal server error",
				Err:        err,
			},
		}, nil
	}

	log.Infow("Successfully updated chat history",
		"history_id", input.HistoryID,
		"user_id", input.UserID)

	// Update user streak asynchronously
	go func(userID uint64, log *zap.SugaredLogger) {
		if err := im.updateUserStreak(userID, log); err != nil {
			log.Errorw("Failed to update user streak", "error", err, "user_id", userID)
		}
	}(input.UserID, log)

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

func (im *InferenceHandler) updateUserStreak(userID uint64, log *zap.SugaredLogger) error {
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
			log.Warnw("Failed to parse last_chat timestamp", "error", parseErr, "value", lastChatStr.String)
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

		log.Infow("Updated user streak", "user_id", userID, "streak", newStreak, "last_chat", now)
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
