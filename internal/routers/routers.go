package routers

import (
	"fmt"
	"io"
	"maps"
	"net/http"

	"sybil-api/internal/ctx"
)

func readRequestBody(c *ctx.Context) ([]byte, error) {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		c.Log.Errorw("Failed to read request body", "error", err.Error())
		return nil, err
	}
	return body, nil
}

func buildLogFields(c *ctx.Context, endpoint string, extras map[string]string) map[string]string {
	fields := map[string]string{
		"endpoint":   endpoint,
		"user_id":    fmt.Sprintf("%d", c.User.UserID),
		"request_id": c.Reqid,
	}
	maps.Copy(fields, extras)
	return fields
}

func setupSSEHeaders(c *ctx.Context) {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
}

func createStreamCallback(c *ctx.Context) func(token string) error {
	return func(token string) error {
		if c.Request().Context().Err() != nil {
			return c.Request().Context().Err()
		}
		_, err := fmt.Fprintf(c.Response(), "%s\n\n", token)
		if err != nil {
			return err
		}
		c.Response().Flush()
		return nil
	}
}
