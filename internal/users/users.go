// Package users
package users

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"api.go/internal/setup"
	"api.go/internal/shared"
)

func GetUserMetadataFromKey(apiKey string, c *setup.Context) (*shared.UserMetadata, error) {
	var userMetadata shared.UserMetadata
	userMetadata.APIKey = apiKey
	ctx := c.Request().Context()

	userInfoCacheKey := fmt.Sprintf("v3:user:apikey:%s", apiKey)
	userInfoCache, err := c.Core.RedisClient.Get(ctx, userInfoCacheKey).Result()
	switch err {
	case nil:
		err = json.Unmarshal([]byte(userInfoCache), &userMetadata)
		if err == nil {
			return &userMetadata, nil
		}
		c.Log.Errorw("Error unmarshalling user info cache", "error", err)
		fallthrough
	default:
		c.Log.Debugw("User cache miss", "key", userInfoCacheKey)

		err = c.Core.RDB.QueryRowContext(ctx, `
		SELECT
		user.id,
		user.email,
		user.plan_id,
		user.plan_credits,
		user.bought_credits,
		user.allow_overspend,
		subscription_plans.requests_per_minute,
		subscription_plans.store_data
		FROM user
		INNER JOIN api_key ON user.id = api_key.user_id
		INNER JOIN subscription_plans ON user.plan_id = subscription_plans.id
		WHERE api_key.id = ? AND subscription_plans.active = 1
		`, apiKey).Scan(
			&userMetadata.UserID,
			&userMetadata.Email,
			&userMetadata.PlanID,
			&userMetadata.PlanCredits,
			&userMetadata.BoughtCredits,
			&userMetadata.AllowOverspend,
			&userMetadata.RPM,
			&userMetadata.StoreData,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				c.Log.Warnw("Invalid API key or inactive plan", "key", apiKey)
				return nil, shared.ErrUnauthorized
			}
			c.Log.Errorw("Database error during API key validation", "error", err)
			return nil, shared.ErrUnauthorized
		}
		go func() {
			userInfoCache, err := json.Marshal(userMetadata)
			if err != nil {
				c.Log.Errorw("Error marshalling user info", "error", err)
				return
			}
			c.Core.RedisClient.Set(ctx, userInfoCacheKey, userInfoCache, 1*time.Minute)
		}()
		return &userMetadata, nil
	}
}
