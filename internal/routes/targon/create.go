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
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"
	"time"

	"github.com/aidarkhanov/nanoid"
	"github.com/labstack/echo/v4"
)

type CreateModelRequest struct {
	BaseModel           string   `json:"base_model"`
	SupportedModelNames []string `json:"supported_model_names,omitempty"`
	AllowedUserID       uint64   `json:"allowed_user_id,omitempty"`
	Modality            string   `json:"modality"`
	SupportedEndpoints  []string `json:"supported_endpoints"`
	Description         string   `json:"description,omitempty"`

	Framework        string            `json:"framework"`
	FrameworkVersion string            `json:"framework_version"`
	Args             []string          `json:"args,omitempty"`
	Env              map[string]string `json:"env,omitempty"`

	ResourceName string `json:"resource_name"`
	MinReplicas  *int   `json:"min_replicas,omitempty"`
	MaxReplicas  int    `json:"max_replicas"`

	ScalingConfig *ScalingConfig `json:"scaling,omitempty"`
	Pricing       *Pricing       `json:"pricing,omitempty"`

	ContainerConcurrency *int64  `json:"containerConcurrency,omitempty"`
	TimeoutSeconds       *int64  `json:"timeoutSeconds,omitempty"`
	SharedMemorySize     *string `json:"shared_memory_size,omitempty"`
}

type ScalingConfig struct {
	ScaleToZeroGracePeriod string            `json:"scale_to_zero_grace_period,omitempty"`
	ScaleUpDelay           string            `json:"scale_up_delay,omitempty"`
	ScaleDownDelay         string            `json:"scale_down_delay,omitempty"`
	TargetConcurrency      *int64            `json:"target_concurrency,omitempty"`
	CustomAnnotations      map[string]string `json:"custom_annotations,omitempty"`
}

type Pricing struct {
	ICPT uint64 `json:"icpt"`
	OCPT uint64 `json:"ocpt"`
	CRC  uint64 `json:"crc"`
}

type TargonCreateRequest struct {
	Name         string                        `json:"name"`
	ResourceName string                        `json:"resource_name"`
	Framework    string                        `json:"framework"`
	Predictor    TargonPredictorConfig         `json:"predictor"`
	Scaling      *TargonInferenceScalingConfig `json:"scaling,omitempty"`
}

type TargonPredictorConfig struct {
	Container            TargonCustomInferenceContainer `json:"container"`
	MinReplicas          *int32                         `json:"minReplicas,omitempty"`
	MaxReplicas          int32                          `json:"maxReplicas"`
	ContainerConcurrency *int64                         `json:"containerConcurrency,omitempty"`
	TimeoutSeconds       *int64                         `json:"timeoutSeconds,omitempty"`
}

type TargonCustomInferenceContainer struct {
	Name             string         `json:"name"`
	Image            string         `json:"image"`
	Command          []string       `json:"command,omitempty"`
	Args             []string       `json:"args,omitempty"`
	WorkingDir       string         `json:"workingDir,omitempty"`
	Ports            []TargonPort   `json:"ports,omitempty"`
	Env              []TargonEnvVar `json:"env,omitempty"`
	SharedMemorySize *string        `json:"shared_memory_size,omitempty"`
}

type TargonPort struct {
	ContainerPort int32  `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

type TargonEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TargonInferenceScalingConfig struct {
	ScaleToZeroGracePeriod string            `json:"scaleToZeroGracePeriod,omitempty"`
	ScaleUpDelay           string            `json:"scaleUpDelay,omitempty"`
	ScaleDownDelay         string            `json:"scaleDownDelay,omitempty"`
	TargetConcurrency      *int64            `json:"targetConcurrency,omitempty"`
	CustomAnnotations      map[string]string `json:"customAnnotations,omitempty"`
}

type TargonServiceResponse struct {
	Message    string `json:"message"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	UID        string `json:"uid"`
	ServiceUID string `json:"service_uid"`
}

type TargonServiceStatusResponse struct {
	UID     string  `json:"uid"`
	Deleted *string `json:"deleted,omitempty"`
	Status  *struct {
		URL   string `json:"url"`
		Ready bool   `json:"ready"`
	} `json:"status"`
}

func (t *TargonManager) CreateModel(cc echo.Context) error {
	c := cc.(*setup.Context)

	var req CreateModelRequest
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		t.Log.Errorw("Failed to read request body", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	if err := json.Unmarshal(body, &req); err != nil {
		t.Log.Errorw("Failed to unmarshal request body", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	if err := validateCreateModelRequest(req); err != nil {
		t.Log.Errorw("Failed to validate request", "error", err.Error())
		return c.JSON(http.StatusBadRequest, err)
	}

	t.Log.Infow("Creating model",
		"name", req.BaseModel,
		"framework", req.Framework,
		"user_id", c.User.UserID)

	targonReq, err := buildTargonRequest(req)
	if err != nil {
		t.Log.Errorw("Failed to build targon request", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}
	t.Log.Infow("Targon request built", "request", targonReq)

	t.Log.Infow("Creating model in Targon", "request", targonReq)
	targonReqJSON, err := json.Marshal(targonReq)
	if err != nil {
		t.Log.Errorw("Failed to marshal targon request", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}
	t.Log.Infow("Targon request marshalled", "request", string(targonReqJSON))

	url := fmt.Sprintf("%s/v1/inference", t.TargonEndpoint)
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(targonReqJSON))
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
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "targon service creation failed"})
	}

	var targonResp TargonServiceResponse
	if err := json.Unmarshal(resBody, &targonResp); err != nil {
		t.Log.Errorw("Failed to parse Targon response", "error", err.Error())
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	t.Log.Infow("Targon service created",
		"targon_uid", targonResp.UID,
		"namespace", targonResp.Namespace)

	icpt, ocpt, crc := uint64(100), uint64(200), uint64(50)
	if req.Pricing != nil {
		icpt = req.Pricing.ICPT
		ocpt = req.Pricing.OCPT
		crc = req.Pricing.CRC
	}

	// Marshal supported_endpoints to JSON
	supportedEndpointsJSON, err := json.Marshal(req.SupportedEndpoints)
	if err != nil {
		t.Log.Errorw("Failed to marshal supported_endpoints", "error", err)
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	var allowedUserID *uint64
	if req.AllowedUserID > 0 {
		allowedUserID = &req.AllowedUserID
	}

	insertModelsQuery := `
		INSERT INTO model (
			name, 
			modality,
			icpt,
			ocpt,
			crc,
			description,
			supported_endpoints,
			allowed_user_id,
			enabled,
			config,
			targon_uid
		) VALUES (
		 ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	result, err := t.WDB.ExecContext(c.Request().Context(), insertModelsQuery, req.BaseModel, req.Modality, icpt, ocpt, crc, req.Description, string(supportedEndpointsJSON), allowedUserID, false, string(targonReqJSON), targonResp.UID)
	if err != nil {
		t.Log.Errorw("Failed to insert model into database", "error", err)
		// Try to cleanup the orphaned Targon service
		err = t.cleanupTargonService(targonResp.UID)
		if err != nil {
			t.Log.Errorw("Failed to cleanup orphaned Targon service", "error", err)
		}
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}
	modelID, err := result.LastInsertId()
	if err != nil {
		t.Log.Errorw("Failed to get last insert id", "error", err)
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}
	t.Log.Infow("Model inserted into database", "model", req.BaseModel, "model_id", modelID)

	modelNames := req.SupportedModelNames
	modelNames = append(modelNames, req.BaseModel)

	go t.pollAndEnableModel(context.Background(), targonResp.UID, modelNames, uint64(modelID),
		icpt, ocpt, crc, req.Modality, req.AllowedUserID)

	return c.JSON(http.StatusOK, map[string]any{
		"model_id":   modelID,
		"targon_uid": targonResp.UID,
		"name":       targonResp.Name,
		"status":     "creating",
		"message":    "Model creation initiated. Polling targon for status.",
	})

}

func validateCreateModelRequest(req CreateModelRequest) error {
	if req.BaseModel == "" {
		return errors.New("name is required")
	}
	if req.Framework != "vllm" && req.Framework != "sglang" {
		return errors.New("framework must be vllm or sglang")
	}
	if req.FrameworkVersion == "" {
		return errors.New("framework_version is required")
	}
	if req.ResourceName == "" {
		return errors.New("resource_name is required")
	}
	if req.MaxReplicas < 1 {
		return errors.New("max_replicas must be at least 1")
	}
	if len(req.SupportedModelNames) == 0 {
		return errors.New("supported_model_names is required and must not be empty")
	}

	// Validate shared memory size format if provided
	if req.SharedMemorySize != nil && *req.SharedMemorySize != "" {
		validSizes := []string{"Gi", "G", "Mi", "M"}
		valid := false
		for _, suffix := range validSizes {
			if strings.HasSuffix(*req.SharedMemorySize, suffix) {
				valid = true
				break
			}
		}
		if !valid {
			return errors.New("shared_memory_size must end with Gi, G, Mi, or M (e.g., '100Gi', '50G')")
		}
	}

	// Validate scaling durations
	if req.ScalingConfig != nil {
		if req.ScalingConfig.ScaleToZeroGracePeriod != "" {
			if _, err := time.ParseDuration(req.ScalingConfig.ScaleToZeroGracePeriod); err != nil {
				return fmt.Errorf("invalid scale_to_zero_grace_period: %w", err)
			}
		}
		if req.ScalingConfig.ScaleUpDelay != "" {
			if _, err := time.ParseDuration(req.ScalingConfig.ScaleUpDelay); err != nil {
				return fmt.Errorf("invalid scale_up_delay: %w", err)
			}
		}
		if req.ScalingConfig.ScaleDownDelay != "" {
			if _, err := time.ParseDuration(req.ScalingConfig.ScaleDownDelay); err != nil {
				return fmt.Errorf("invalid scale_down_delay: %w", err)
			}
		}
	}

	return nil
}
func buildTargonRequest(req CreateModelRequest) (TargonCreateRequest, error) {
	// Build image
	version := req.FrameworkVersion
	if version == "" {
		version = "latest"
	}

	var image string
	var defaultCommand []string
	switch strings.ToLower(req.Framework) {
	case "vllm":
		image = fmt.Sprintf("vllm/vllm-openai:%s", version)
		defaultCommand = []string{"python3", "-m", "vllm.entrypoints.openai.api_server"}
	case "sglang":
		image = fmt.Sprintf("lmsysorg/sglang:%s", version)
		defaultCommand = []string{"python3", "-m", "sglang.launch_server"}
	default:
		image = fmt.Sprintf("vllm/vllm-openai:%s", version)
		defaultCommand = []string{"python3", "-m", "vllm.entrypoints.openai.api_server"}
	}

	// Convert env to Targon format
	var envVars []TargonEnvVar
	for k, v := range req.Env {
		envVars = append(envVars, TargonEnvVar{Name: k, Value: v})
	}

	// Get framework port
	var port int32
	switch strings.ToLower(req.Framework) {
	case "vllm":
		port = 8000
	case "sglang":
		port = 30000
	default:
		port = 8080
	}

	// Replicas
	minReplicas := int32(0)
	if req.MinReplicas != nil {
		minReplicas = int32(*req.MinReplicas)
	}
	maxReplicas := int32(req.MaxReplicas)

	// Scaling config
	var scaling *TargonInferenceScalingConfig
	if req.ScalingConfig != nil {
		scaling = &TargonInferenceScalingConfig{
			ScaleToZeroGracePeriod: req.ScalingConfig.ScaleToZeroGracePeriod,
			ScaleUpDelay:           req.ScalingConfig.ScaleUpDelay,
			ScaleDownDelay:         req.ScalingConfig.ScaleDownDelay,
			TargetConcurrency:      req.ScalingConfig.TargetConcurrency,
			CustomAnnotations:      req.ScalingConfig.CustomAnnotations,
		}
	}

	// Auto-set shared memory if not provided - default to 100Gi for multi-GPU workloads
	sharedMemorySize := req.SharedMemorySize
	if sharedMemorySize == nil || *sharedMemorySize == "" {
		// Check if this is likely a multi-GPU workload by looking at args
		isMultiGPU := false
		for i, arg := range req.Args {
			if (arg == "--tp-size" || arg == "--tensor-parallel-size") && i+1 < len(req.Args) {
				if req.Args[i+1] != "1" {
					isMultiGPU = true
					break
				}
			}
		}

		if isMultiGPU {
			defaultSize := "100Gi"
			sharedMemorySize = &defaultSize
		}
	}

	sybilID, err := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 10)
	if err != nil {
		return TargonCreateRequest{}, errors.New("failed to generate nanoid")
	}
	sybilName := fmt.Sprintf("sybil-%s", sybilID)

	containerName := fmt.Sprintf("%s-%s", sybilName, req.BaseModel)
	return TargonCreateRequest{
		Name:         sybilName,
		ResourceName: req.ResourceName,
		Framework:    req.Framework,
		Predictor: TargonPredictorConfig{
			Container: TargonCustomInferenceContainer{
				Name:             containerName,
				Image:            image,
				Command:          defaultCommand,
				Args:             req.Args,
				Env:              envVars,
				Ports:            []TargonPort{{ContainerPort: port, Protocol: "TCP"}},
				SharedMemorySize: sharedMemorySize, // Pass it to Targon
			},
			MinReplicas:          &minReplicas,
			MaxReplicas:          maxReplicas,
			ContainerConcurrency: req.ContainerConcurrency,
			TimeoutSeconds:       req.TimeoutSeconds,
		},
		Scaling: scaling,
	}, nil
}

func (t *TargonManager) pollAndEnableModel(ctx context.Context, targonUID string, modelNames []string, modelID uint64,
	icpt, ocpt, crc uint64, modality string, allowedUserID uint64) {
	ticker := time.NewTicker(shared.TargonPollingInterval)
	defer ticker.Stop()

	maxAttempts := shared.PollingMaxAttempts
	attempts := 0

	for {
		select {
		case <-ctx.Done():
			t.Log.Warnw("Polling canceled by context",
				"model_id", modelID,
				"targon_uid", targonUID)
			return
		case <-ticker.C:
			attempts++
			if attempts > maxAttempts {
				t.Log.Errorw("Polling timeout for model",
					"model_id", modelID,
					"targon_uid", targonUID)
				return
			}

			url := fmt.Sprintf("%s/v1/inference/%s", t.TargonEndpoint, targonUID)
			httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				t.Log.Errorw("Failed to create http request", "error", err.Error())
				continue
			}
			httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.TargonAPIKey))
			httpReq.Header.Set("Content-Type", "application/json")

			res, err := t.HTTPClient.Do(httpReq)
			if err != nil {
				t.Log.Errorw("Failed to do http request", "error", err.Error())
				continue
			}

			resBody, err := io.ReadAll(res.Body)
			if closeErr := res.Body.Close(); closeErr != nil {
				t.Log.Warnw("Failed to close response body in polling", "error", closeErr)
			}
			if err != nil {
				t.Log.Errorw("Failed to read response body", "error", err.Error())
				continue
			}

			if res.StatusCode != http.StatusOK {
				t.Log.Warnw("Targon returned non-OK status",
					"status", res.StatusCode,
					"body", string(resBody),
					"targon_uid", targonUID)
				continue
			}

			var targonResp TargonServiceStatusResponse
			if err := json.Unmarshal(resBody, &targonResp); err != nil {
				t.Log.Errorw("Failed to parse Targon response", "error", err.Error())
				continue
			}

			// Check if service was deleted
			if targonResp.Deleted != nil && *targonResp.Deleted != "" {
				t.Log.Warnw("Targon service has been deleted, stopping polling",
					"model_id", modelID,
					"targon_uid", targonUID,
					"deleted_at", *targonResp.Deleted)

				// Mark model as disabled in database
				_, err := t.WDB.ExecContext(ctx,
					"UPDATE model SET enabled = false WHERE id = ?",
					modelID)
				if err != nil {
					t.Log.Errorw("Failed to disable deleted model",
						"error", err,
						"model_id", modelID)
				}
				return
			}

			// Check if service is ready and has URL
			if targonResp.Status != nil && targonResp.Status.Ready && targonResp.Status.URL != "" {
				t.Log.Infow("Service is ready",
					"targon_uid", targonUID,
					"url", targonResp.Status.URL)

				// Insert into model_registry for each supported model
				for _, modelName := range modelNames {
					regQuery := `
						INSERT INTO model_registry (model_id, model_name, url)
						VALUES (?, ?, ?)
						ON DUPLICATE KEY UPDATE url = VALUES(url)
					`
					_, err := t.WDB.ExecContext(ctx, regQuery, modelID, modelName, targonResp.Status.URL)
					if err != nil {
						t.Log.Errorw("Failed to insert into model_registry",
							"error", err,
							"model_name", modelName)
						continue
					}

					t.Log.Infow("Model registered",
						"model_name", modelName,
						"url", targonResp.Status.URL)

					// cache full model details
					cacheKey := fmt.Sprintf("v1:model:service:%s", modelName)
					serviceCache := map[string]any{
						"model_id":        modelID,
						"url":             targonResp.Status.URL,
						"icpt":            icpt,
						"ocpt":            ocpt,
						"crc":             crc,
						"modality":        modality,
						"allowed_user_id": allowedUserID,
					}
					cacheJSON, err := json.Marshal(serviceCache)
					if err != nil {
						t.Log.Warnw("Failed to marshal service cache",
							"error", err,
							"model_name", modelName)
						continue
					}

					if err := t.RedisClient.Set(ctx, cacheKey, cacheJSON, shared.ModelServiceCacheTTL).Err(); err != nil {
						t.Log.Warnw("Failed to cache model service in Redis",
							"error", err,
							"model_name", modelName,
							"cache_key", cacheKey)
					} else {
						t.Log.Infow("Model service cached in Redis",
							"model_name", modelName,
							"url", targonResp.Status.URL,
							"ttl", "30m")
					}
				}

				// Update models table to enabled=true
				_, updateErr := t.WDB.ExecContext(ctx, "UPDATE model SET enabled = true WHERE id = ?", modelID)
				if updateErr != nil {
					t.Log.Errorw("Failed to update model enabled status", "error", updateErr, "model_id", modelID)
				}

				t.Log.Infow("Targon model is ready and enabled", "targon_uid", targonUID, "model_id", modelID)
				return
			}

			t.Log.Infow("Targon model is not ready", "targon_uid", targonUID)
		}
	}
}
