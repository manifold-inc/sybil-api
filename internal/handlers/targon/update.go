package targon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sybil-api/internal/shared"
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

// UpdateModelInput contains all data needed for UpdateModel business logic
type UpdateModelInput struct {
	Ctx    context.Context
	UserID uint64
	Req    UpdateModelRequest
}

type UpdateModelOutput struct {
	ModelID   uint64
	TargonUID string
	Name      *string
	Message   string
}

func (t *TargonHandler) UpdateModelLogic(input UpdateModelInput) (*UpdateModelOutput, error) {
	if err := validateUpdateModelRequest(input.Req); err != nil {
		return nil, errors.Join(errors.New("failed to validate request"), err, shared.ErrBadRequest)
	}

	// Verify the model exists and get current config
	var modelID uint64
	var currentTargonUID string
	var currentConfigJSON string
	checkQuery := `SELECT id, targon_uid, config FROM model WHERE targon_uid = ?`
	err := t.WDB.QueryRowContext(input.Ctx, checkQuery, input.Req.TargonUID).Scan(&modelID, &currentTargonUID, &currentConfigJSON)
	if err != nil {
		return nil, shared.ErrNotFound
	}

	// Parse current config
	var currentConfig TargonCreateRequest
	if err := json.Unmarshal([]byte(currentConfigJSON), &currentConfig); err != nil {
		return nil, errors.Join(errors.New("failed to parse current config"), err, shared.ErrInternalServerError)
	}

	// Build the Targon update request
	targonReq := buildTargonUpdateRequest(input.Req)
	targonReqJSON, err := json.Marshal(targonReq)
	if err != nil {
		return nil, errors.Join(errors.New("failed to marshal targon request"), err, shared.ErrInternalServerError)
	}

	// Send update request to Targon
	url := fmt.Sprintf("%s/v1/inference", t.TargonEndpoint)
	httpReq, err := http.NewRequest("PATCH", url, bytes.NewBuffer(targonReqJSON))
	if err != nil {
		return nil, errors.Join(errors.New("failed creating http request"), err, shared.ErrInternalServerError)
	}
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.TargonAPIKey))
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := t.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, errors.Join(errors.New("failed to do http request"), err, shared.ErrInternalServerError)
	}
	defer func() {
		if closeErr := res.Body.Close(); closeErr != nil {
			t.Log.Warnw("failed to close response body", "error", closeErr)
		}
	}()

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, errors.Join(errors.New("failed to read response body"), err, shared.ErrInternalServerError)
	}

	if res.StatusCode != http.StatusOK {
		return nil, errors.Join(fmt.Errorf("targon returned error: [%d: %s]", res.StatusCode, string(resBody)), err, shared.ErrInternalServerError)
	}

	var targonResp map[string]any
	if err := json.Unmarshal(resBody, &targonResp); err != nil {
		return nil, errors.Join(errors.New("failed to parse targon response"), err, shared.ErrInternalServerError)
	}

	t.Log.Infow("Targon service updated successfully",
		"targon_uid", input.Req.TargonUID,
		"model_id", modelID)

	// Merge updates into current config to maintain full configuration
	mergedConfig := mergeConfigs(currentConfig, input.Req)
	mergedConfigJSON, err := json.Marshal(mergedConfig)
	if err != nil {
		return nil, errors.Join(errors.New("failed to marshal merged config"), err, shared.ErrInternalServerError)
	}

	// Update database record
	var args []any
	var setFields []string

	// Always update config with merged full config
	setFields = append(setFields, "config = ?")
	args = append(args, string(mergedConfigJSON))

	if input.Req.Name != nil && *input.Req.Name != "" {
		setFields = append(setFields, "name = ?")
		args = append(args, *input.Req.Name)
	}

	args = append(args, input.Req.TargonUID)

	updateQuery := fmt.Sprintf(`
		UPDATE model 
		SET %s
		WHERE targon_uid = ?
	`, strings.Join(setFields, ", "))

	_, err = t.WDB.ExecContext(input.Ctx, updateQuery, args...)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to update model database record: [%s:%d]", input.Req.TargonUID, modelID), err, shared.ErrPartialSuccess)
	}

	response := map[string]any{
		"message":    "Successfully updated model",
		"targon_uid": input.Req.TargonUID,
		"model_id":   modelID,
	}

	if input.Req.Name != nil {
		response["name"] = *input.Req.Name
	}

	return &UpdateModelOutput{
		ModelID:   modelID,
		TargonUID: input.Req.TargonUID,
		Name:      input.Req.Name,
		Message:   "Successfully updated model",
	}, nil
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

	return nil
}

func mergeConfigs(currentConfig TargonCreateRequest, updateReq UpdateModelRequest) TargonCreateRequest {
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

func buildTargonUpdateRequest(req UpdateModelRequest) TargonUpdateRequest {
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
