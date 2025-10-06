package inference

import (
	"context"
	"encoding/json"
	"fmt"
)

type InferenceService struct {
	UID         string `json:"uid"`
	UserID      uint64 `json:"user_id"`
	ServiceName string `json:"service_name"`
	URL         string `json:"url"`
	Cost        uint64 `json:"cost"`
	ICPT        uint64 `json:"icpt"`
	OCPT        uint64 `json:"ocpt"`
	CRC         uint64 `json:"crc"`
}

func (im *InferenceManager) DiscoverModels(ctx context.Context, userID uint64, modelURL string) (*InferenceService, error) {

	// Try redis cache first
	cacheKey := fmt.Sprintf("v4:inference:discovery:%d:%s", userID, modelURL)
	cached, err := im.RedisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		var inferenceService InferenceService
		if err := json.Unmarshal([]byte(cached), &inferenceService); err == nil {
			im.Log.Debugw("Cache hit", "key", cacheKey)
			return &inferenceService, nil
		}
		im.Log.Warnw("Error unmarshalling inference service from cache", "error", err)
	}

	// cache miss, query db
	im.Log.Debugw("Cache miss, querying database", "key", cacheKey)

	/*
			query := `
				SELECT
					uid,
					user_id,
					service_name,
					cost
				FROM inference_services
				WHERE user_id = ?
				AND deleted IS NULL
				LIMIT 1
			`

		var service InferenceService
		err = im.RDB.QueryRowContext(ctx, query, userID).Scan(
			&service.UID,
			&service.UserID,
			&service.ServiceName,
			&service.Cost,
		)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("service not found: %s", modelURL)
		}
		if err != nil {
			return nil, fmt.Errorf("database error: %w", err)
		}

		// TODO: find a better way to calculate cost
		service.ICPT = service.Cost * 15
		service.OCPT = service.Cost * 30
		service.CRC = service.Cost * 150

		// save to redis
		cacheValue, err := json.Marshal(service)
		if err != nil {
			return nil, fmt.Errorf("error marshalling inference service to cache: %w", err)
		}
		err = im.RedisClient.Set(ctx, cacheKey, cacheValue, 60*time.Second).Err()
		if err != nil {
			return nil, fmt.Errorf("error saving inference service to cache: %w", err)
		}

		return &service, nil
	*/
	return nil, nil
}
