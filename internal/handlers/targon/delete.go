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

func (t *TargonHandler) DeleteModelLogic(input DeleteModelInput) DeleteModelOutput {

	checkQuery := `SELECT id FROM model WHERE targon_uid = ?`
	var modelID uint64
	err := t.RDB.QueryRowContext(input.Ctx, checkQuery, input.ModelUID).Scan(&modelID)
	if err != nil {
		t.Log.Errorw("Failed to find model", "error", err, "targon_uid", input.ModelUID)
		return DeleteModelOutput{Error: errors.New("Model not found"), StatusCode: 404}
	}

	// cache clear
	rows, err := t.RDB.Query("SELECT model_name FROM model_registry WHERE model_id = ?", modelID)
	if err != nil {
		t.Log.Errorw("Failed to get model names", "error", err, "model_id", modelID)
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
		t.Log.Errorw("Failed to soft delete model", "error", err, "targon_uid", input.ModelUID)
		return DeleteModelOutput{Error: errors.New("Failed to soft delete model"), StatusCode: 500}
	}

	_, err = t.WDB.ExecContext(input.Ctx,
		"DELETE FROM model_registry WHERE model_id = ?", modelID)
	if err != nil {
		t.Log.Errorw("Failed to delete from model registry", "error", err, "model_id", modelID)
		return DeleteModelOutput{Error: errors.New("Failed to delete model registry"), StatusCode: 500}
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
				t.Log.Warnw("Failed to clear cache for deleted model", "error", err, "model_id", mid)
			} else {
				t.Log.Infow("Cleared cache for deleted model", "model_id", mid, "aliases_cleared", len(cacheKeys))
			}
		}
	}(modelNames, modelID)

	t.Log.Infow("Successfully deleted model",
		"targon_uid", input.ModelUID,
		"model_id", modelID,
		"model_names", modelNames)

	return DeleteModelOutput{
		ModelID:    modelID,
		TargonUID:  input.ModelUID,
		ModelNames: modelNames,
		Message:    "Model deleted successfully",
		StatusCode: 200,
	}
}

// clean up orphaned Targon service if anything goes wrong
func (t *TargonHandler) cleanupTargonService(targonUID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), shared.TargonCleanupTimeout)
	defer cancel()

	url := fmt.Sprintf("%s/v1/inference/%s", t.TargonEndpoint, targonUID)
	httpReq, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		t.Log.Errorw("Failed to create cleanup http request",
			"error", err.Error(),
			"targon_uid", targonUID)
		return err
	}
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.TargonAPIKey))

	res, err := t.HTTPClient.Do(httpReq)
	if err != nil {
		t.Log.Errorw("Failed to cleanup orphaned Targon service",
			"error", err.Error(),
			"targon_uid", targonUID)
		return err
	}
	defer func() {
		if closeErr := res.Body.Close(); closeErr != nil {
			t.Log.Warnw("Failed to close response body during cleanup", "error", closeErr)
		}
	}()

	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		resBody, _ := io.ReadAll(res.Body)
		t.Log.Errorw("Targon cleanup returned error",
			"status", res.StatusCode,
			"body", string(resBody),
			"targon_uid", targonUID)
	} else {
		t.Log.Infow("Successfully cleaned up orphaned Targon service",
			"targon_uid", targonUID)
	}

	return nil
}
