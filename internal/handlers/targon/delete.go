// Package targon
package targon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"sybil-api/internal/shared"
)

// DeleteModelInput contains all data needed for DeleteModel business logic
type DeleteModelInput struct {
	Ctx      context.Context
	UserID   uint64
	ModelUID string
}

type DeleteModelOutput struct {
	ModelID    uint64
	TargonUID  string
	ModelNames []string
	Message    string
}

func (t *TargonHandler) DeleteModelLogic(input DeleteModelInput) (*DeleteModelOutput, error) {
	checkQuery := `SELECT id FROM model WHERE targon_uid = ?`
	var modelID uint64
	err := t.RDB.QueryRowContext(input.Ctx, checkQuery, input.ModelUID).Scan(&modelID)
	if err != nil {
		return nil, errors.Join(errors.New("failed to find model"), err, shared.ErrNotFound)
	}

	// cache clear
	rows, err := t.RDB.Query("SELECT model_name FROM model_registry WHERE model_id = ?", modelID)
	if err != nil {
		// can safely fail, only used for clearing redis cache
		t.Log.Warnw("failed to get model names", "error", err, "model_id", modelID)
	}

	var modelNames []string
	if rows != nil {
		for rows.Next() {
			var modelName string
			if err := rows.Scan(&modelName); err == nil {
				modelNames = append(modelNames, modelName)
			}
		}
		rows.Close()
	}

	// delete from targon
	err = t.cleanupTargonService(input.ModelUID)
	if err != nil {
		t.Log.Warnw("Failed to delete from Targon, continuing with local cleanup",
			"error", err,
			"targon_uid", input.ModelUID)
	}

	_, err = t.WDB.ExecContext(input.Ctx,
		"UPDATE model SET enabled = false WHERE targon_uid = ?", input.ModelUID)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to soft delete model %s", input.ModelUID), err, shared.ErrInternalServerError)
	}

	_, err = t.WDB.ExecContext(input.Ctx,
		"DELETE FROM model_registry WHERE model_id = ?", modelID)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to delete from model registry: %d", modelID), err, shared.ErrInternalServerError)
	}

	// cache clear
	go func(names []string, mid uint64) {
		ctx := context.Background()
		var cacheKeys []string
		for _, modelName := range names {
			cacheKey := fmt.Sprintf("sybil:v1:model:service:%s", modelName)
			cacheKeys = append(cacheKeys, cacheKey)
		}

		if len(cacheKeys) > 0 {
			if err := t.RedisClient.Del(ctx, cacheKeys...).Err(); err != nil {
				t.Log.Warnw("failed to clear cache for deleted model", "error", err, "model_id", mid)
			}
		}
	}(modelNames, modelID)

	return &DeleteModelOutput{
		ModelID:    modelID,
		TargonUID:  input.ModelUID,
		ModelNames: modelNames,
		Message:    "Model deleted successfully",
	}, nil
}

// clean up orphaned Targon service if anything goes wrong
func (t *TargonHandler) cleanupTargonService(targonUID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), shared.TargonCleanupTimeout)
	defer cancel()

	url := fmt.Sprintf("%s/v1/inference/%s", t.TargonEndpoint, targonUID)
	httpReq, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return errors.Join(fmt.Errorf("failed to create cleanup http request: %s", targonUID), err)
	}
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.TargonAPIKey))

	res, err := t.HTTPClient.Do(httpReq)
	if err != nil {
		return errors.Join(fmt.Errorf("failed to clean up orphaned targon service: %s", targonUID), err)
	}
	defer func() {
		if closeErr := res.Body.Close(); closeErr != nil {
			t.Log.Warnw("failed to close response body during cleanup", "error", closeErr)
		}
	}()

	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		resBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("targon returned error: [%d: %s]", res.StatusCode, string(resBody))
	}

	return nil
}
