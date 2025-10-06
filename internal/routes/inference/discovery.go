package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type InferenceService struct {
	ModelID  uint64 `json:"model_id"`
	URL      string `json:"url"`
	ICPT     uint64 `json:"icpt"`
	OCPT     uint64 `json:"ocpt"`
	CRC      uint64 `json:"crc"`
	Private  bool   `json:"private"`
	Modality string `json:"modality"`
}

// DiscoverModels finds the inference service URL and pricing for a given model name
func (im *InferenceManager) DiscoverModels(ctx context.Context, userID uint64, modelName string) (*InferenceService, error) {
	// 1. Try Redis cache first (fast path - no DB query needed!)
	cacheKey := fmt.Sprintf("model:service:%s", modelName)
	cached, err := im.RedisClient.Get(ctx, cacheKey).Result()
	if err == nil && cached != "" {
		var serviceCache map[string]interface{}
		if err := json.Unmarshal([]byte(cached), &serviceCache); err == nil {
			im.Log.Debugw("Cache hit for model service", "model_name", modelName)

			// Parse cached data
			service := &InferenceService{
				ModelID:  uint64(serviceCache["model_id"].(float64)),
				URL:      serviceCache["url"].(string),
				ICPT:     uint64(serviceCache["icpt"].(float64)),
				OCPT:     uint64(serviceCache["ocpt"].(float64)),
				CRC:      uint64(serviceCache["crc"].(float64)),
				Private:  serviceCache["private"].(bool),
				Modality: serviceCache["modality"].(string),
			}

			// Check permissions for private models
			if service.Private {
				if allowedUserIDFloat, ok := serviceCache["allowed_user_id"].(float64); ok {
					allowedUserID := uint64(allowedUserIDFloat)
					if allowedUserID != userID {
						im.Log.Warnw("Access denied to private model (from cache)",
							"model_name", modelName,
							"user_id", userID,
							"allowed_user_id", allowedUserID)
						return nil, fmt.Errorf("access denied: model is private")
					}
				} else {
					// allowed_user_id is nil, deny access
					return nil, fmt.Errorf("access denied: model is private")
				}
			}

			im.Log.Debugw("Model service retrieved from cache",
				"model_name", modelName,
				"model_id", service.ModelID)
			return service, nil
		}
		im.Log.Warnw("Failed to unmarshal cached model service", "error", err, "model_name", modelName)
	}

	// 2. Cache miss or error - query database (slow path)
	im.Log.Debugw("Cache miss, querying database", "model_name", modelName)

	query := `
		SELECT 
			mr.url,
			m.id,
			m.icpt,
			m.ocpt,
			m.crc,
			m.private,
			m.modality,
			m.allowed_user_id
		FROM model_registry mr
		INNER JOIN models m ON mr.model_id = m.id
		WHERE mr.model_name = ? 
		AND m.enabled = true
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
		&service.Private,
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
	if service.Private {
		if allowedUserID == nil || *allowedUserID != userID {
			im.Log.Warnw("Access denied to private model",
				"model_name", modelName,
				"user_id", userID,
				"allowed_user_id", allowedUserID)
			return nil, fmt.Errorf("access denied: model is private")
		}
	}

	// 3. Cache full service in Redis (30 minute TTL)
	go func() {
		serviceCache := map[string]interface{}{
			"model_id":        service.ModelID,
			"url":             service.URL,
			"icpt":            service.ICPT,
			"ocpt":            service.OCPT,
			"crc":             service.CRC,
			"private":         service.Private,
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

		if err := im.RedisClient.Set(context.Background(), cacheKey, cacheJSON, 30*time.Minute).Err(); err != nil {
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
