// Package targon
package targon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

func (t *TargonManager) DeleteModel(cc echo.Context) error {
	c := cc.(*setup.Context)

	modelUID := c.Param("uid")

	checkQuery := `SELECT id FROM model WHERE targon_uid = ?`
	var modelID uint64
	err := t.RDB.QueryRowContext(c.Request().Context(), checkQuery, modelUID).Scan(&modelID)
	if err != nil {
		t.Log.Errorw("Failed to find model", "error", err, "targon_uid", modelUID)
		return c.JSON(404, "Model not found")
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
	err = t.cleanupTargonService(modelUID)
	if err != nil {
		t.Log.Warnw("Failed to delete from Targon, continuing with local cleanup",
			"error", err,
			"targon_uid", modelUID)
	}

	_, err = t.WDB.ExecContext(c.Request().Context(),
		"UPDATE model SET enabled = false WHERE targon_uid = ?", modelUID)
	if err != nil {
		t.Log.Errorw("Failed to soft delete model", "error", err, "targon_uid", modelUID)
		return c.JSON(500, map[string]string{"error": "Failed to soft delete model"})
	}

	_, err = t.WDB.ExecContext(c.Request().Context(),
		"DELETE FROM model_registry WHERE model_id = ?", modelID)
	if err != nil {
		t.Log.Errorw("Failed to delete from model registry", "error", err, "model_id", modelID)
		return c.JSON(500, map[string]string{"error": "Failed to delete model registry"})
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
		"targon_uid", modelUID,
		"model_id", modelID,
		"model_names", modelNames)

	return c.JSON(200, map[string]any{
		"message":     "Model deleted successfully",
		"targon_uid":  modelUID,
		"model_id":    modelID,
		"model_names": modelNames,
	})
}

// clean up orphaned Targon service if anything goes wrong
func (t *TargonManager) cleanupTargonService(targonUID string) error {
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
