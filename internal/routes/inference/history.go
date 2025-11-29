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

	historyIDNano, err := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 11)
	if err != nil {
		c.Log.Errorw("Failed to generate history nanoid", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to generate history ID"})
	}
	historyID := "chat-" + historyIDNano

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

	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		c.Log.Errorw("Failed to marshal initial messages", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to prepare history"})
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

	_, err = im.WDB.Exec(insertQuery,
		c.User.UserID,
		historyID,
		string(messagesJSON),
		title,
		nil, // icon
	)
	if err != nil {
		c.Log.Errorw("Failed to insert history into database", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create history"})
	}

	c.Log.Infow("Chat history created", "history_id", historyID, "user_id", c.User.UserID)

	c.Response().Header().Set("Content-Type", "text/event-stream")
	historyIDEvent := map[string]any{
		"type": "history_id",
		"id":   historyID,
	}
	historyIDJSON, _ := json.Marshal(historyIDEvent)
	fmt.Fprintf(c.Response(), "data: %s\n\n", string(historyIDJSON))
	c.Response().Flush()

	c.Request().Body = io.NopCloser(strings.NewReader(string(body)))

	responseContent, err := im.CompletionRequestHistory(c)

	statusCode := c.Response().Status
	if statusCode >= 400 {
		c.Log.Warnw("Not updating history due to error status code", "status_code", statusCode)
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

	allMessagesJSON, err := json.Marshal(allMessages)
	if err != nil {
		c.Log.Errorw("Failed to marshal complete messages", "error", err)
		return nil
	}

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
	}(c.User.UserID, historyID, allMessagesJSON, c.Log)

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
	return extractContentFromStreamingResponse(responseContent)
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
	var currentStreak uint64
	var lastChat sql.NullTime

	err := im.RDB.QueryRow(`
		SELECT last_chat, streak 
		FROM user 
		WHERE id = ?
	`, userID).Scan(&lastChat, &currentStreak)

	if err != nil {
		var lastChatStr sql.NullString
		err = im.RDB.QueryRow(`
			SELECT last_chat, streak 
			FROM user 
			WHERE id = ?
		`, userID).Scan(&lastChatStr, &currentStreak)
		if err != nil {
			return fmt.Errorf("failed to get user streak data: %w", err)
		}

		if lastChatStr.Valid && lastChatStr.String != "" {
			formats := []string{
				"2006-01-02 15:04:05",
				time.RFC3339,
				"2006-01-02T15:04:05Z07:00",
				"2006-01-02 15:04:05.000000",
				"2006-01-02T15:04:05Z",
				"2006-01-02 15:04:05.000000000",
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
				log.Errorw("Failed to parse last_chat timestamp, preserving current streak",
					"error", parseErr,
					"value", lastChatStr.String,
					"current_streak", currentStreak)
				_, err = im.WDB.Exec(`
					UPDATE user 
					SET last_chat = ? 
					WHERE id = ?
				`, time.Now().UTC(), userID)
				if err != nil {
					return fmt.Errorf("failed to update last_chat after parse error: %w", err)
				}
				return nil
			}
		}
	}

	now := time.Now().UTC()
	todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var newStreak uint64
	updateStreak := false

	if lastChat.Valid {
		lastChatDate := lastChat.Time.UTC()
		lastChatMidnight := time.Date(lastChatDate.Year(), lastChatDate.Month(), lastChatDate.Day(), 0, 0, 0, 0, time.UTC)

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
