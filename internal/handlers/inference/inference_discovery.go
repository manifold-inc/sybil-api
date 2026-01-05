package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"sybil-api/internal/shared"
)

type InferenceService struct {
	ModelID  uint64 `json:"model_id"`
	URL      string `json:"url"`
	ICPT     uint64 `json:"icpt"`
	OCPT     uint64 `json:"ocpt"`
	CRC      uint64 `json:"crc"`
	Modality string `json:"modality"`
}

func (im *InferenceHandler) DiscoverModels(ctx context.Context, userID uint64, modelName string) (*InferenceService, error) {
	cacheKey := fmt.Sprintf("sybil:v1:model:service:%d:%s", userID, modelName)
	cached, err := im.RedisClient.Get(ctx, cacheKey).Result()
	if err == nil && cached != "" {
		var serviceCache map[string]any
		if err := json.Unmarshal([]byte(cached), &serviceCache); err == nil {
			im.Log.Debugw("Cache hit for model service", "model_name", modelName, "user_id", userID)

			// parse cached data
			service := &InferenceService{
				ModelID:  uint64(serviceCache["model_id"].(float64)),
				URL:      serviceCache["url"].(string),
				ICPT:     uint64(serviceCache["icpt"].(float64)),
				OCPT:     uint64(serviceCache["ocpt"].(float64)),
				CRC:      uint64(serviceCache["crc"].(float64)),
				Modality: serviceCache["modality"].(string),
			}

			im.Log.Debugw("Model service retrieved from cache",
				"model_name", modelName,
				"model_id", service.ModelID,
				"user_id", userID)
			return service, nil
		}
		im.Log.Warnw("Failed to unmarshal cached model service", "error", err, "model_name", modelName)
	}

	im.Log.Debugw("Cache miss, querying database", "model_name", modelName)

	query := `
		SELECT 
			model_registry.url,
			model.id,
			model.icpt,
			model.ocpt,
			model.crc,
			model.modality,
			model.allowed_user_id
		FROM model_registry
		INNER JOIN model ON model_registry.model_id = model.id
		WHERE model_registry.model_name = ? 
		AND model.enabled = true
		AND (model.allowed_user_id = ? OR model.allowed_user_id IS NULL)
		ORDER BY model.allowed_user_id DESC
		LIMIT 1
	`

	var service InferenceService
	var allowedUserID *uint64
	err = im.RDB.QueryRowContext(ctx, query, modelName, userID).Scan(
		&service.URL,
		&service.ModelID,
		&service.ICPT,
		&service.OCPT,
		&service.CRC,
		&service.Modality,
		&allowedUserID,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("model not found or not enabled: %s", modelName)
	}
	if err != nil {
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Check permissions for private models
	if allowedUserID != nil {
		if *allowedUserID != userID {
			return nil, errors.New("user not authorized for this model")
		}
	}

	// cache full service
	go func() {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		serviceCache := map[string]any{
			"model_id": service.ModelID,
			"url":      service.URL,
			"icpt":     service.ICPT,
			"ocpt":     service.OCPT,
			"crc":      service.CRC,
			"modality": service.Modality,
		}
		cacheJSON, err := json.Marshal(serviceCache)
		if err != nil {
			im.Log.Warnw("Failed to marshal service for cache",
				"error", err,
				"model_name", modelName)
			return
		}

		if err := im.RedisClient.Set(cacheCtx, cacheKey, cacheJSON, shared.ModelServiceCacheTTL).Err(); err != nil {
			im.Log.Warnw("Failed to cache model service",
				"error", err,
				"model_name", modelName,
				"cache_key", cacheKey)
		}
	}()

	return &service, nil
}
