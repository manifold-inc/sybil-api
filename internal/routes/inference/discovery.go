package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sybil-api/internal/shared"
	"time"
)

type InferenceService struct {
	ModelID  uint64 `json:"model_id"`
	URL      string `json:"url"`
	ICPT     uint64 `json:"icpt"`
	OCPT     uint64 `json:"ocpt"`
	CRC      uint64 `json:"crc"`
	Modality string `json:"modality"`
}

func (im *InferenceManager) DiscoverModels(ctx context.Context, userID uint64, modelName string) (*InferenceService, error) {
	cacheKey := fmt.Sprintf("sybil:v1:model:service:%s", modelName)
	cached, err := im.RedisClient.Get(ctx, cacheKey).Result()
	if err == nil && cached != "" {
		var serviceCache map[string]any
		if err := json.Unmarshal([]byte(cached), &serviceCache); err == nil {
			im.Log.Debugw("Cache hit for model service", "model_name", modelName)

			// parse cached data
			service := &InferenceService{
				ModelID:  uint64(serviceCache["model_id"].(float64)),
				URL:      serviceCache["url"].(string),
				ICPT:     uint64(serviceCache["icpt"].(float64)),
				OCPT:     uint64(serviceCache["ocpt"].(float64)),
				CRC:      uint64(serviceCache["crc"].(float64)),
				Modality: serviceCache["modality"].(string),
			}

			// check permissions for private models (indicated by non-null allowed_user_id)
			if allowedUserIDFloat, ok := serviceCache["allowed_user_id"].(float64); ok && allowedUserIDFloat > 0 {
				// This is a private model with a specific allowed user
				allowedUserID := uint64(allowedUserIDFloat)
				if allowedUserID != userID {
					im.Log.Warnw("Access denied to private model (from cache)",
						"model_name", modelName,
						"user_id", userID,
						"allowed_user_id", allowedUserID)
					return nil, fmt.Errorf("access denied: model is private")
				}
			}
			// If allowed_user_id is null/missing/0, it's a public model - allow access

			im.Log.Debugw("Model service retrieved from cache",
				"model_name", modelName,
				"model_id", service.ModelID)
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
		LIMIT 1
	`

	var service InferenceService
	var allowedUserID *uint64
	err = im.RDB.QueryRowContext(ctx, query, modelName).Scan(
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
		im.Log.Errorw("Database error during model discovery", "error", err, "model_name", modelName)
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Check permissions for private models
	if allowedUserID != nil {
		if *allowedUserID != userID {
			im.Log.Warnw("Access denied to private model",
				"model_name", modelName,
				"user_id", userID,
				"allowed_user_id", allowedUserID)
			return nil, fmt.Errorf("access denied: model is private")
		}
	}

	// cache full service
	go func() {

		cacheCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		serviceCache := map[string]any{
			"model_id":        service.ModelID,
			"url":             service.URL,
			"icpt":            service.ICPT,
			"ocpt":            service.OCPT,
			"crc":             service.CRC,
			"modality":        service.Modality,
			"allowed_user_id": allowedUserID,
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

	im.Log.Infow("Model discovered",
		"model_name", modelName,
		"model_id", service.ModelID,
		"url", service.URL)

	return &service, nil
}
