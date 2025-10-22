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

	checkQuery := `SELECT id, targon_uid FROM model WHERE targon_uid = ? AND enabled = true`
	var modelID uint64
	var targonUID string
	err := t.WDB.QueryRowContext(c.Request().Context(), checkQuery, modelUID).Scan(&modelID, &targonUID)
	if err != nil {
		return c.JSON(404, "Model not found or not enabled")
	}

	// Delete from Targon
	err = t.cleanupTargonService(targonUID)
	if err != nil {
		return c.JSON(500, "Failed to delete from Targon")
	}

	// Delete from database
	_, err = t.WDB.Exec("UPDATE model SET enabled = false WHERE targon_uid = ?", targonUID)
	if err != nil {
		return c.JSON(500, "Failed to delete from database")
	}

	return c.JSON(200, "Model deleted successfully")
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
