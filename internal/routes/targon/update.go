package targon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"
	"time"

	"github.com/labstack/echo/v4"
)

type UpdateModelRequest struct {
	TargonUID    string           `json:"targon_uid"`
	Name         *string          `json:"name,omitempty"`
	ResourceName *string          `json:"resource_name,omitempty"`
	Predictor    *PredictorUpdate `json:"predictor,omitempty"`
	Scaling      *ScalingConfig   `json:"scaling,omitempty"`
}

type PredictorUpdate struct {
	Container            *ContainerUpdate `json:"container,omitempty"`
	MinReplicas          *int32           `json:"minReplicas,omitempty"`
	MaxReplicas          *int32           `json:"maxReplicas,omitempty"`
	ContainerConcurrency *int64           `json:"containerConcurrency,omitempty"`
	TimeoutSeconds       *int64           `json:"timeoutSeconds,omitempty"`
}

type ContainerUpdate struct {
	Name             *string         `json:"name,omitempty"`
	Image            *string         `json:"image,omitempty"`
	Command          *[]string       `json:"command,omitempty"`
	Args             *[]string       `json:"args,omitempty"`
	WorkingDir       *string         `json:"workingDir,omitempty"`
	Ports            *[]TargonPort   `json:"ports,omitempty"`
	Env              *[]TargonEnvVar `json:"env,omitempty"`
	SharedMemorySize *string         `json:"shared_memory_size,omitempty"`
	ReadinessProbe   *Probes         `json:"readinessProbe,omitempty"`
	LivenessProbe    *Probes         `json:"livenessProbe,omitempty"`
}

// TargonUpdateRequest is what gets sent to Targon API
type TargonUpdateRequest struct {
	InferenceUID string                        `json:"inference_uid"`
	Name         string                        `json:"name,omitempty"`
	ResourceName string                        `json:"resource_name,omitempty"`
	Predictor    *TargonPredictorConfigUpdate  `json:"predictor,omitempty"`
	Scaling      *TargonInferenceScalingConfig `json:"scaling,omitempty"`
}

type TargonPredictorConfigUpdate struct {
	Container            *TargonCustomInferenceContainer `json:"container,omitempty"`
	MinReplicas          *int32                          `json:"minReplicas,omitempty"`
	MaxReplicas          int32                           `json:"maxReplicas,omitempty"`
	ContainerConcurrency *int64                          `json:"containerConcurrency,omitempty"`
	TimeoutSeconds       *int64                          `json:"timeoutSeconds,omitempty"`
}

func (t *TargonManager) UpdateModel(cc echo.Context) error {
	c := cc.(*setup.Context)

	var req UpdateModelRequest
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		t.Log.Errorw("Failed to read request body", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	if err := json.Unmarshal(body, &req); err != nil {
		t.Log.Errorw("Failed to unmarshal request body", "error", err.Error())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	if err := validateUpdateModelRequest(req); err != nil {
		t.Log.Errorw("Failed to validate request", "error", err.Error())
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// Verify the model exists and get current config
	var modelID uint64
	var currentTargonUID string
	var currentConfigJSON string
	checkQuery := `SELECT id, targon_uid, config FROM model WHERE targon_uid = ?`
	err = t.WDB.QueryRowContext(c.Request().Context(), checkQuery, req.TargonUID).Scan(&modelID, &currentTargonUID, &currentConfigJSON)
	if err != nil {
		t.Log.Errorw("Model not found or access denied", "error", err.Error(), "targon_uid", req.TargonUID)
		return c.JSON(http.StatusNotFound, map[string]string{"error": "model not found"})
	}

	// Parse current config
	var currentConfig TargonCreateRequest
	if err := json.Unmarshal([]byte(currentConfigJSON), &currentConfig); err != nil {
		t.Log.Errorw("Failed to parse current config", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	t.Log.Infow("Updating model",
		"targon_uid", req.TargonUID,
		"model_id", modelID,
		"user_id", c.User.UserID)

	var port int32 = currentConfig.Predictor.Container.Ports[0].ContainerPort
	// Build the Targon update request
	targonReq := buildTargonUpdateRequest(req, port)
	targonReqJSON, err := json.Marshal(targonReq)
	if err != nil {
		t.Log.Errorw("Failed to marshal targon request", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	t.Log.Infow("Sending update request to Targon", "request", string(targonReqJSON))

	// Send update request to Targon
	url := fmt.Sprintf("%s/v1/inference", t.TargonEndpoint)
	httpReq, err := http.NewRequest("PATCH", url, bytes.NewBuffer(targonReqJSON))
	if err != nil {
		t.Log.Errorw("Failed to create http request", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.TargonAPIKey))
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := t.HTTPClient.Do(httpReq)
	if err != nil {
		t.Log.Errorw("Failed to do http request", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}
	defer func() {
		if closeErr := res.Body.Close(); closeErr != nil {
			t.Log.Warnw("Failed to close response body", "error", closeErr)
		}
	}()

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		t.Log.Errorw("Failed to read response body", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	if res.StatusCode != http.StatusOK {
		t.Log.Errorw("Targon returned error", "status", res.StatusCode, "body", string(resBody))
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "targon service update failed", "details": string(resBody)})
	}

	var targonResp map[string]any
	if err := json.Unmarshal(resBody, &targonResp); err != nil {
		t.Log.Errorw("Failed to parse Targon response", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	t.Log.Infow("Targon service updated successfully",
		"targon_uid", req.TargonUID,
		"model_id", modelID)

	// Merge updates into current config to maintain full configuration
	mergedConfig := mergeConfigs(currentConfig, req, port)
	mergedConfigJSON, err := json.Marshal(mergedConfig)
	if err != nil {
		t.Log.Errorw("Failed to marshal merged config", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	// Update database record
	var args []any
	var setFields []string

	// Always update config with merged full config
	setFields = append(setFields, "config = ?")
	args = append(args, string(mergedConfigJSON))

	if req.Name != nil && *req.Name != "" {
		setFields = append(setFields, "name = ?")
		args = append(args, *req.Name)
	}

	args = append(args, req.TargonUID)

	updateQuery := fmt.Sprintf(`
		UPDATE model 
		SET %s
		WHERE targon_uid = ?
	`, strings.Join(setFields, ", "))

	_, err = t.WDB.ExecContext(c.Request().Context(), updateQuery, args...)
	if err != nil {
		t.Log.Errorw("Failed to update model database record",
			"error", err.Error(),
			"targon_uid", req.TargonUID,
			"model_id", modelID)
		return c.JSON(http.StatusOK, map[string]any{
			"message":    "Model updated in Targon but failed to update database record. Please contact support.",
			"targon_uid": req.TargonUID,
		})
	}

	t.Log.Infow("Successfully updated model",
		"targon_uid", req.TargonUID,
		"model_id", modelID,
		"user_id", c.User.UserID)

	response := map[string]any{
		"message":    "Successfully updated model",
		"targon_uid": req.TargonUID,
		"model_id":   modelID,
	}

	if req.Name != nil {
		response["name"] = *req.Name
	}

	return c.JSON(http.StatusOK, response)
}

func validateUpdateModelRequest(req UpdateModelRequest) error {
	if req.TargonUID == "" {
		return errors.New("targon_uid is required")
	}

	// Validate shared memory size format if provided
	if req.Predictor != nil && req.Predictor.Container != nil &&
		req.Predictor.Container.SharedMemorySize != nil && *req.Predictor.Container.SharedMemorySize != "" {
		validSizes := []string{"Gi", "G", "Mi", "M"}
		valid := false
		for _, suffix := range validSizes {
			if strings.HasSuffix(*req.Predictor.Container.SharedMemorySize, suffix) {
				valid = true
				break
			}
		}
		if !valid {
			return errors.New("shared_memory_size must end with Gi, G, Mi, or M (e.g., '100Gi', '50G')")
		}
	}

	// Validate scaling durations
	if req.Scaling != nil {
		if req.Scaling.ScaleToZeroGracePeriod != "" {
			if _, err := time.ParseDuration(req.Scaling.ScaleToZeroGracePeriod); err != nil {
				return fmt.Errorf("invalid scale_to_zero_grace_period: %w", err)
			}
		}
		if req.Scaling.ScaleUpDelay != "" {
			if _, err := time.ParseDuration(req.Scaling.ScaleUpDelay); err != nil {
				return fmt.Errorf("invalid scale_up_delay: %w", err)
			}
		}
		if req.Scaling.ScaleDownDelay != "" {
			if _, err := time.ParseDuration(req.Scaling.ScaleDownDelay); err != nil {
				return fmt.Errorf("invalid scale_down_delay: %w", err)
			}
		}
	}

	// Validate replicas
	if req.Predictor != nil {
		if req.Predictor.MaxReplicas != nil && *req.Predictor.MaxReplicas < 1 {
			return errors.New("maxReplicas must be at least 1")
		}
		if req.Predictor.MinReplicas != nil && *req.Predictor.MinReplicas < 0 {
			return errors.New("minReplicas cannot be negative")
		}
		if req.Predictor.MinReplicas != nil && req.Predictor.MaxReplicas != nil {
			if *req.Predictor.MinReplicas > *req.Predictor.MaxReplicas {
				return errors.New("minReplicas cannot be greater than maxReplicas")
			}
		}
	}

	// Validate readiness and liveness probe
	validProbeEndpoints := map[string]bool{
		"/health": true,
	}

	if req.Predictor != nil && req.Predictor.Container != nil {
		if req.Predictor.Container.ReadinessProbe != nil {
			readyP := req.Predictor.Container.ReadinessProbe
			if readyP.Endpoint == "" {
				return errors.New("readinessProbe endpoint cannot be empty")
			}
			if !validProbeEndpoints[readyP.Endpoint] {
				return fmt.Errorf("invalid readinessProbe endpoint: %s. Valid endpoints are: /health", readyP.Endpoint)
			}
		}

		if req.Predictor.Container.LivenessProbe != nil {
			liveP := req.Predictor.Container.LivenessProbe
			if liveP.Endpoint == "" {
				return errors.New("livenessProbe endpoint cannot be empty")
			}
			if !validProbeEndpoints[liveP.Endpoint] {
				return fmt.Errorf("invalid livenessProbe endpoint: %s. Valid endpoints are: /health", liveP.Endpoint)
			}
		}
	}

	return nil
}

func mergeConfigs(currentConfig TargonCreateRequest, updateReq UpdateModelRequest, port int32) TargonCreateRequest {
	// Start with current config
	merged := currentConfig

	// Update name if provided
	if updateReq.Name != nil && *updateReq.Name != "" {
		merged.Name = *updateReq.Name
	}

	// Update resource name if provided
	if updateReq.ResourceName != nil && *updateReq.ResourceName != "" {
		merged.ResourceName = *updateReq.ResourceName
	}

	// Update predictor fields if provided
	if updateReq.Predictor != nil {
		if updateReq.Predictor.MinReplicas != nil {
			merged.Predictor.MinReplicas = updateReq.Predictor.MinReplicas
		}
		if updateReq.Predictor.MaxReplicas != nil {
			merged.Predictor.MaxReplicas = *updateReq.Predictor.MaxReplicas
		}
		if updateReq.Predictor.ContainerConcurrency != nil {
			merged.Predictor.ContainerConcurrency = updateReq.Predictor.ContainerConcurrency
		}
		if updateReq.Predictor.TimeoutSeconds != nil {
			merged.Predictor.TimeoutSeconds = updateReq.Predictor.TimeoutSeconds
		}

		// Update container fields if provided
		if updateReq.Predictor.Container != nil {
			if updateReq.Predictor.Container.Name != nil {
				merged.Predictor.Container.Name = *updateReq.Predictor.Container.Name
			}
			if updateReq.Predictor.Container.Image != nil {
				merged.Predictor.Container.Image = *updateReq.Predictor.Container.Image
			}
			if updateReq.Predictor.Container.Command != nil {
				merged.Predictor.Container.Command = *updateReq.Predictor.Container.Command
			}
			if updateReq.Predictor.Container.Args != nil {
				merged.Predictor.Container.Args = *updateReq.Predictor.Container.Args
			}
			if updateReq.Predictor.Container.WorkingDir != nil {
				merged.Predictor.Container.WorkingDir = *updateReq.Predictor.Container.WorkingDir
			}
			if updateReq.Predictor.Container.Ports != nil {
				merged.Predictor.Container.Ports = *updateReq.Predictor.Container.Ports
			}
			if updateReq.Predictor.Container.Env != nil {
				merged.Predictor.Container.Env = *updateReq.Predictor.Container.Env
			}
			if updateReq.Predictor.Container.SharedMemorySize != nil {
				merged.Predictor.Container.SharedMemorySize = updateReq.Predictor.Container.SharedMemorySize
			}
			if updateReq.Predictor.Container.ReadinessProbe != nil {
				merged.Predictor.Container.ReadinessProbe = toTargonProbe(updateReq.Predictor.Container.ReadinessProbe, port)
			}
			if updateReq.Predictor.Container.LivenessProbe != nil {
				merged.Predictor.Container.LivenessProbe = toTargonProbe(updateReq.Predictor.Container.LivenessProbe, port)
			}
		}
	}

	// Update scaling config if provided
	if updateReq.Scaling != nil {
		if merged.Scaling == nil {
			merged.Scaling = &TargonInferenceScalingConfig{}
		}
		if updateReq.Scaling.ScaleToZeroGracePeriod != "" {
			merged.Scaling.ScaleToZeroGracePeriod = updateReq.Scaling.ScaleToZeroGracePeriod
		}
		if updateReq.Scaling.ScaleUpDelay != "" {
			merged.Scaling.ScaleUpDelay = updateReq.Scaling.ScaleUpDelay
		}
		if updateReq.Scaling.ScaleDownDelay != "" {
			merged.Scaling.ScaleDownDelay = updateReq.Scaling.ScaleDownDelay
		}
		if updateReq.Scaling.TargetConcurrency != nil {
			merged.Scaling.TargetConcurrency = updateReq.Scaling.TargetConcurrency
		}
		// Replace custom annotations entirely if provided (including empty map to clear them)
		if updateReq.Scaling.CustomAnnotations != nil {
			merged.Scaling.CustomAnnotations = updateReq.Scaling.CustomAnnotations
		}
	}

	return merged
}

func buildTargonUpdateRequest(req UpdateModelRequest, port int32) TargonUpdateRequest {
	targonReq := TargonUpdateRequest{
		InferenceUID: req.TargonUID,
	}

	if req.Name != nil {
		targonReq.Name = *req.Name
	}

	if req.ResourceName != nil {
		targonReq.ResourceName = *req.ResourceName
	}

	if req.Predictor != nil {
		predictorUpdate := &TargonPredictorConfigUpdate{}

		if req.Predictor.Container != nil {
			container := &TargonCustomInferenceContainer{}

			if req.Predictor.Container.Name != nil {
				container.Name = *req.Predictor.Container.Name
			}
			if req.Predictor.Container.Image != nil {
				container.Image = *req.Predictor.Container.Image
			}
			if req.Predictor.Container.Command != nil {
				container.Command = *req.Predictor.Container.Command
			}
			if req.Predictor.Container.Args != nil {
				container.Args = *req.Predictor.Container.Args
			}
			if req.Predictor.Container.WorkingDir != nil {
				container.WorkingDir = *req.Predictor.Container.WorkingDir
			}
			if req.Predictor.Container.Ports != nil {
				container.Ports = *req.Predictor.Container.Ports
			}
			if req.Predictor.Container.Env != nil {
				container.Env = *req.Predictor.Container.Env
			}
			if req.Predictor.Container.SharedMemorySize != nil {
				container.SharedMemorySize = req.Predictor.Container.SharedMemorySize
			}
			if req.Predictor.Container.ReadinessProbe != nil {
				container.ReadinessProbe = toTargonProbe(req.Predictor.Container.ReadinessProbe, port)
			}
			if req.Predictor.Container.LivenessProbe != nil {
				container.LivenessProbe = toTargonProbe(req.Predictor.Container.LivenessProbe, port)
			}

			predictorUpdate.Container = container
		}

		if req.Predictor.MinReplicas != nil {
			predictorUpdate.MinReplicas = req.Predictor.MinReplicas
		}
		if req.Predictor.MaxReplicas != nil {
			predictorUpdate.MaxReplicas = *req.Predictor.MaxReplicas
		}
		if req.Predictor.ContainerConcurrency != nil {
			predictorUpdate.ContainerConcurrency = req.Predictor.ContainerConcurrency
		}
		if req.Predictor.TimeoutSeconds != nil {
			predictorUpdate.TimeoutSeconds = req.Predictor.TimeoutSeconds
		}

		targonReq.Predictor = predictorUpdate
	}

	if req.Scaling != nil {
		targonReq.Scaling = &TargonInferenceScalingConfig{
			ScaleToZeroGracePeriod: req.Scaling.ScaleToZeroGracePeriod,
			ScaleUpDelay:           req.Scaling.ScaleUpDelay,
			ScaleDownDelay:         req.Scaling.ScaleDownDelay,
			TargetConcurrency:      req.Scaling.TargetConcurrency,
			CustomAnnotations:      req.Scaling.CustomAnnotations,
		}
	}

	return targonReq
}
