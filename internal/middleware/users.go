package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"sybil-api/internal/shared"
)

func (u *UserManager) getUserMetadataFromKey(apiKey string, ctx context.Context) (*shared.UserMetadata, error) {
	var userMetadata shared.UserMetadata
	userMetadata.APIKey = apiKey

	userInfoCacheKey := fmt.Sprintf("v4:user:apikey:%s", apiKey)
	userInfoCache, err := u.redis.Get(ctx, userInfoCacheKey).Result()
	switch err {
	case nil:
		err = json.Unmarshal([]byte(userInfoCache), &userMetadata)
		if err == nil {
			return &userMetadata, nil
		}
		u.log.Errorw("Error unmarshalling user info cache", "error", err)
		fallthrough
	default:
		u.log.Debugw("User cache miss", "key", userInfoCacheKey)

		err = u.rdb.QueryRowContext(ctx, `
		SELECT
		user.id,
		user.email,
		user.credits,
		user.allow_overspend,
		user.role
		FROM user
		INNER JOIN api_key ON user.id = api_key.user_id
		WHERE api_key.id = ?
		`, apiKey).Scan(
			&userMetadata.UserID,
			&userMetadata.Email,
			&userMetadata.Credits,
			&userMetadata.AllowOverspend,
			&userMetadata.Role,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				u.log.Warnw("Invalid API key or inactive plan", "key", apiKey)
				return nil, shared.ErrUnauthorized
			}
			u.log.Errorw("Database error during API key validation", "error", err)
			return nil, shared.ErrUnauthorized
		}
		go func() {
			userInfoCache, err := json.Marshal(userMetadata)
			if err != nil {
				u.log.Errorw("Error marshalling user info", "error", err)
				return
			}
			u.redis.Set(ctx, userInfoCacheKey, userInfoCache, shared.UserInfoCacheTTL)
		}()
		return &userMetadata, nil
	}
}
