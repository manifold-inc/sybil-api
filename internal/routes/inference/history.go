package inference

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"
	"time"

	"github.com/aidarkhanov/nanoid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

type CreateHistoryRequest struct {
	Messages []shared.ChatMessage `json:"messages"`
}

type UpdateHistoryRequest struct {
	Messages []shared.ChatMessage `json:"messages,omitempty"`
}

func (im *InferenceManager) CompletionRequestNewHistory(cc echo.Context) error {
	c := cc.(*setup.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.Log.Errorw("Failed to read request body", "error", err.Error())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
	}

	var payload shared.InferenceBody
	if err := json.Unmarshal(body, &payload); err != nil {
		c.Log.Errorw("Failed to parse request body", "error", err.Error())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	if len(payload.Messages) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "messages are required"})
	}

	messages := payload.Messages
	providedHistoryID := payload.HistoryID

	c.Request().Body = io.NopCloser(strings.NewReader(string(body)))

	responseContent, err := im.CompletionRequestHistory(c)

	statusCode := c.Response().Status
	if statusCode >= 400 {
		c.Log.Warnw("Not saving history due to error status code", "status_code", statusCode)
		if c.Response().Committed {
			return nil
		}
		if err != nil {
			return err
		}
		return nil
	}

	if err != nil {
		if c.Response().Committed {
			return nil
		}
		return err
	}

	var allMessages []shared.ChatMessage
	allMessages = append(allMessages, messages...)

	if content := extractContentFromResponse(responseContent); content != "" {
		allMessages = append(allMessages, shared.ChatMessage{
			Role:    "assistant",
			Content: content,
		})
	}

	messagesJSON, err := json.Marshal(allMessages)
	if err != nil {
		c.Log.Errorw("Failed to marshal messages", "error", err)
		return nil
	}

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

	go func(userID uint64, providedHistoryID *string, messagesJSON []byte, title *string, log *zap.SugaredLogger) {
		var historyID string
		if providedHistoryID != nil {
			historyID = *providedHistoryID
		} else {
			historyIDNano, err := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 27)
			if err != nil {
				log.Errorw("Failed to generate history nanoid", "error", err)
				return
			}
			historyID = "chat-" + historyIDNano
		}

		insertQuery := `
			INSERT INTO chat_history (
				user_id,
				history_id,
				messages,
				title,
				icon
			) VALUES (?, ?, ?, ?, ?)
		`

		_, err := im.WDB.Exec(insertQuery,
			userID,
			historyID,
			string(messagesJSON),
			title,
			nil, // icon
		)
		if err != nil {
			log.Errorw("Failed to insert history into database", "error", err)
			return
		}

		log.Infow("Chat history created", "history_id", historyID, "user_id", userID)

		if err := im.updateUserStreak(userID, log); err != nil {
			log.Errorw("Failed to update user streak", "error", err, "user_id", userID)
		}
	}(c.User.UserID, providedHistoryID, messagesJSON, title, c.Log)

	return nil
}

func (im *InferenceManager) UpdateHistory(cc echo.Context) error {
	c := cc.(*setup.Context)

	historyIDStr := c.Param("history_id")

	var userID uint64
	checkQuery := `SELECT user_id FROM chat_history WHERE history_id = ?`
	err := im.RDB.QueryRowContext(c.Request().Context(), checkQuery, historyIDStr).Scan(&userID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.Log.Errorw("History not found", "error", err.Error(), "history_id", historyIDStr)
			return c.JSON(http.StatusNotFound, map[string]string{"error": "history not found"})
		}
		c.Log.Errorw("Failed to check history", "error", err.Error(), "history_id", historyIDStr)
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	if userID != c.User.UserID {
		c.Log.Errorw("Unauthorized access to history", "history_id", historyIDStr, "user_id", c.User.UserID, "owner_id", userID)
		return c.JSON(http.StatusForbidden, map[string]string{"error": "unauthorized"})
	}

	var req UpdateHistoryRequest
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.Log.Errorw("Failed to read request body", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	if err := json.Unmarshal(body, &req); err != nil {
		c.Log.Errorw("Failed to unmarshal request body", "error", err.Error())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	c.Log.Infow("Updating chat history",
		"history_id", historyIDStr,
		"user_id", c.User.UserID)

	if len(req.Messages) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "messages cannot be empty"})
	}

	messagesJSON, err := json.Marshal(req.Messages)
	if err != nil {
		c.Log.Errorw("Failed to marshal messages", "error", err)
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	updateQuery := `
		UPDATE chat_history 
		SET messages = ?, updated_at = NOW()
		WHERE history_id = ?
	`

	_, err = im.WDB.ExecContext(c.Request().Context(), updateQuery, string(messagesJSON), historyIDStr)
	if err != nil {
		c.Log.Errorw("Failed to update history in database",
			"error", err.Error(),
			"history_id", historyIDStr)
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	c.Log.Infow("Successfully updated chat history",
		"history_id", historyIDStr,
		"user_id", c.User.UserID)

	go func(userID uint64, log *zap.SugaredLogger) {
		if err := im.updateUserStreak(userID, log); err != nil {
			log.Errorw("Failed to update user streak", "error", err, "user_id", userID)
		}
	}(c.User.UserID, c.Log)

	return c.JSON(http.StatusOK, map[string]any{
		"message": "History updated successfully",
		"id":      historyIDStr,
		"user_id": c.User.UserID,
	})
}

func extractContentFromResponse(responseContent string) string {
	if responseContent == "" {
		return ""
	}
	if content := extractContentFromSingleResponse(responseContent); content != "" {
		return content
	}
	return extractContentFromStreamingResponse(responseContent)
}

func extractContentFromSingleResponse(responseContent string) string {
	var response shared.Response
	if err := json.Unmarshal([]byte(responseContent), &response); err != nil {
		return ""
	}

	if len(response.Choices) == 0 {
		return ""
	}

	choice := response.Choices[0]
	if choice.Message == nil {
		return ""
	}

	return choice.Message.Content
}

func extractContentFromStreamingResponse(responseContent string) string {
	var chunks []shared.Response
	if err := json.Unmarshal([]byte(responseContent), &chunks); err != nil {
		return ""
	}

	var fullContent strings.Builder
	for _, chunk := range chunks {
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

func (im *InferenceManager) updateUserStreak(userID uint64, log *zap.SugaredLogger) error {
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
