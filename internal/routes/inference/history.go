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

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

type CreateHistoryRequest struct {
	Messages []shared.ChatMessage `json:"messages"`
	Title    *string              `json:"title,omitempty"`
	Icon     *string              `json:"icon,omitempty"`
}

type UpdateHistoryRequest struct {
	Messages []shared.ChatMessage `json:"messages,omitempty"`
	Title    *string              `json:"title,omitempty"`
	Icon     *string              `json:"icon,omitempty"`
}

func (im *InferenceManager) CompletionRequestNewHistory(cc echo.Context) error {
	c := cc.(*setup.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.Log.Errorw("Failed to read request body", "error", err.Error())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
	}

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
	if err != nil {
		return err
	}

	var allMessages []shared.ChatMessage
	allMessages = append(allMessages, messages...)

	if responseContent != "" {
		if content := extractContentFromResponse(responseContent); content != "" {
			allMessages = append(allMessages, shared.ChatMessage{
				Role:    "assistant",
				Content: content,
			})
		}
	}

	messagesJSON, err := json.Marshal(allMessages)
	if err != nil {
		c.Log.Errorw("Failed to marshal messages", "error", err)
		return nil
	}

	go func(userID uint64, messagesJSON []byte, log *zap.SugaredLogger) {
		insertQuery := `
			INSERT INTO history (
				user_id,
				messages,
				title,
				icon
			) VALUES (?, ?, ?, ?)
		`

		result, err := im.WDB.Exec(insertQuery,
			userID,
			string(messagesJSON),
			nil, // title
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
	}(c.User.UserID, messagesJSON, c.Log)

	return nil
}

func (im *InferenceManager) UpdateHistory(cc echo.Context) error {
	c := cc.(*setup.Context)

	historyIDStr := c.Param("history_id")
	historyID, err := strconv.ParseUint(historyIDStr, 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid history_id"})
	}

	var userID uint64
	checkQuery := `SELECT user_id FROM history WHERE id = ?`
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

	if req.Title != nil {
		if len(*req.Title) > 255 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "title must be 255 characters or less"})
		}
		setFields = append(setFields, "title = ?")
		args = append(args, *req.Title)
	}

	if req.Icon != nil {
		if len(*req.Icon) > 50 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "icon must be 50 characters or less"})
		}
		setFields = append(setFields, "icon = ?")
		args = append(args, *req.Icon)
	}

	if len(setFields) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	args = append(args, historyID)

	updateQuery := fmt.Sprintf(`
		UPDATE history 
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

	return c.JSON(http.StatusOK, map[string]any{
		"message": "History updated successfully",
		"id":      historyID,
		"user_id": c.User.UserID,
	})
}

func extractContentFromResponse(responseContent string) string {
	if content := extractContentFromSingleResponse(responseContent); content != "" {
		return content
	}
	return extractContentFromStreamingResponse(responseContent)
}

func extractContentFromSingleResponse(responseContent string) string {
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
