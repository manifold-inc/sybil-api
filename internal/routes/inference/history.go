package inference

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"
	"time"

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

	// Directly unmarshal into the correct type
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		c.Log.Errorw("Failed to parse request body", "error", err.Error())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	messagesRaw, ok := payload["messages"]
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "messages are required"})
	}

	messagesBytes, err := json.Marshal(messagesRaw)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid messages format"})
	}

	var messages []shared.ChatMessage
	if err := json.Unmarshal(messagesBytes, &messages); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid messages format"})
	}

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

	go func(userID uint64, messagesJSON []byte, title *string, log *zap.SugaredLogger) {
		insertQuery := `
			INSERT INTO chat_history (
				user_id,
				messages,
				title,
				icon
			) VALUES (?, ?, ?, ?)
		`

		result, err := im.WDB.Exec(insertQuery,
			userID,
			string(messagesJSON),
			title,
			nil, // icon
		)
		if err != nil {
			log.Errorw("Failed to insert history into database", "error", err)
			return
		}

		historyID, err := result.LastInsertId()
		if err != nil {
			log.Errorw("Failed to get last insert id", "error", err)
			return
		}

		log.Infow("Chat history created", "history_id", historyID, "user_id", userID)

		if err := im.updateUserStreak(userID, log); err != nil {
			log.Errorw("Failed to update user streak", "error", err, "user_id", userID)
		}
	}(c.User.UserID, messagesJSON, title, c.Log)

	return nil
}

func (im *InferenceManager) UpdateHistory(cc echo.Context) error {
	c := cc.(*setup.Context)

	// TODO @sean history IDs should be nanoids
	historyIDStr := c.Param("history_id")
	historyID, err := strconv.ParseUint(historyIDStr, 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid history_id"})
	}

	var userID uint64
	checkQuery := `SELECT user_id FROM chat_history WHERE id = ?`
	err = im.RDB.QueryRowContext(c.Request().Context(), checkQuery, historyID).Scan(&userID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.Log.Errorw("History not found", "error", err.Error(), "history_id", historyID)
			return c.JSON(http.StatusNotFound, map[string]string{"error": "history not found"})
		}
		c.Log.Errorw("Failed to check history", "error", err.Error(), "history_id", historyID)
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	if userID != c.User.UserID {
		c.Log.Errorw("Unauthorized access to history", "history_id", historyID, "user_id", c.User.UserID, "owner_id", userID)
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
		"history_id", historyID,
		"user_id", c.User.UserID)

	var setFields []string
	var args []any

	if req.Messages != nil {
		if len(req.Messages) == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "messages cannot be empty"})
		}
		messagesJSON, err := json.Marshal(req.Messages)
		if err != nil {
			c.Log.Errorw("Failed to marshal messages", "error", err)
			return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
		}
		setFields = append(setFields, "messages = ?")
		args = append(args, string(messagesJSON))
	}

	if len(setFields) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	args = append(args, historyID)

	// TODO @sean ONLY update the history
	updateQuery := fmt.Sprintf(`
		UPDATE chat_history 
		SET %s
		WHERE id = ?
	`, strings.Join(setFields, ", "))

	_, err = im.WDB.ExecContext(c.Request().Context(), updateQuery, args...)
	if err != nil {
		c.Log.Errorw("Failed to update history in database",
			"error", err.Error(),
			"history_id", historyID)
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	c.Log.Infow("Successfully updated chat history",
		"history_id", historyID,
		"user_id", c.User.UserID)

	go func(userID uint64, log *zap.SugaredLogger) {
		if err := im.updateUserStreak(userID, log); err != nil {
			log.Errorw("Failed to update user streak", "error", err, "user_id", userID)
		}
	}(c.User.UserID, c.Log)

	return c.JSON(http.StatusOK, map[string]any{
		"message": "History updated successfully",
		"id":      historyID,
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
	// TODO @sean unmarshal directly into proper struct
	var response map[string]any
	if err := json.Unmarshal([]byte(responseContent), &response); err != nil {
		return ""
	}

	choices, ok := getSlice(response, "choices")
	if !ok || len(choices) == 0 {
		return ""
	}

	choice, ok := getMap(choices[0])
	if !ok {
		return ""
	}

	message, ok := getMap(choice["message"])
	if !ok {
		return ""
	}

	content, ok := message["content"].(string)
	if !ok {
		return ""
	}

	return content
}

func extractContentFromStreamingResponse(responseContent string) string {
	// TODO @sean unmarshal directly into proper struct
	var chunks []map[string]any
	if err := json.Unmarshal([]byte(responseContent), &chunks); err != nil {
		return ""
	}

	var fullContent strings.Builder
	for _, chunk := range chunks {
		choices, ok := getSlice(chunk, "choices")
		if !ok || len(choices) == 0 {
			continue
		}

		choice, ok := getMap(choices[0])
		if !ok {
			continue
		}

		delta, ok := getMap(choice["delta"])
		if !ok {
			continue
		}

		if content, ok := delta["content"].(string); ok {
			fullContent.WriteString(content)
		}
	}

	return fullContent.String()
}

func getSlice(m map[string]any, key string) ([]any, bool) {
	val, ok := m[key]
	if !ok {
		return nil, false
	}
	slice, ok := val.([]any)
	return slice, ok
}

func getMap(val any) (map[string]any, bool) {
	if val == nil {
		return nil, false
	}
	m, ok := val.(map[string]any)
	return m, ok
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
