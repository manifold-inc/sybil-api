package routers

import (
	"fmt"
	"maps"

	"sybil-api/internal/ctx"
)

func buildLogFields(c *ctx.Context, endpoint string, extras map[string]string) map[string]string {
	fields := map[string]string{
		"endpoint":   endpoint,
		"user_id":    fmt.Sprintf("%d", c.User.UserID),
		"request_id": c.Reqid,
	}
	maps.Copy(fields, extras)
	return fields
}

