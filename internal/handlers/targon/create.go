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

	"github.com/aidarkhanov/nanoid"
)

type CreateModelRequest struct {
	BaseModel           string         `json:"base_model"`
	SupportedModelNames []string       `json:"supported_model_names,omitempty"`
	AllowedUserID       uint64         `json:"allowed_user_id,omitempty"`
	Modality            string         `json:"modality"`
	SupportedEndpoints  []string       `json:"supported_endpoints"`
	Description         string         `json:"description,omitempty"`
	Metadata            *ModelMetadata `json:"metadata,omitempty"`

	Framework        string            `json:"framework"`
	FrameworkVersion string            `json:"framework_version"`
	Args             []string          `json:"args,omitempty"`
	Env              map[string]string `json:"env,omitempty"`

	ResourceName string `json:"resource_name"`
	MinReplicas  *int   `json:"minReplicas,omitempty"`
	MaxReplicas  int    `json:"maxReplicas"`

	ScalingConfig *ScalingConfig `json:"scaling,omitempty"`
	Pricing       *Pricing       `json:"pricing,omitempty"`

	ContainerConcurrency *int64  `json:"containerConcurrency,omitempty"`
	TimeoutSeconds       *int64  `json:"timeoutSeconds,omitempty"`
	SharedMemorySize     *string `json:"shared_memory_size,omitempty"`
}

type ScalingConfig struct {
	ScaleToZeroGracePeriod string            `json:"scaleToZeroGracePeriod,omitempty"`
	ScaleUpDelay           string            `json:"scaleUpDelay,omitempty"`
	ScaleDownDelay         string            `json:"scaleDownDelay,omitempty"`
	TargetConcurrency      *int64            `json:"targetConcurrency,omitempty"`
	CustomAnnotations      map[string]string `json:"customAnnotations,omitempty"`
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

type ModelMetadata struct {
	Name                        string   `json:"name"`
	Quantization                string   `json:"quantization,omitempty"`
	ContextLength               int      `json:"context_length,omitempty"`
	MaxOutputLength             int      `json:"max_output_length,omitempty"`
	SupportedSamplingParameters []string `json:"supported_sampling_parameters,omitempty"`
	SupportedFeatures           []string `json:"supported_features,omitempty"`
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

// CreateModelInput contains all data needed for CreateModel business logic
type CreateModelInput struct {
	Ctx    context.Context
	UserID uint64
	Req    CreateModelRequest
}

type CreateModelOutput struct {
	ModelID   int64
	TargonUID string
	Name      string
	Status    string
	Message   string
}

func (t *TargonHandler) CreateModelLogic(input CreateModelInput) (*CreateModelOutput, error) {
	if err := validateCreateModelRequest(input.Req); err != nil {
		return nil, errors.Join(errors.New("failed validating request"), err, shared.ErrBadRequest)
	}

	targonReq, err := buildTargonRequest(input.Req)
	if err != nil {
		return nil, errors.Join(errors.New("failed to build targon request"), err, shared.ErrInternalServerError)
	}
	targonReqJSON, err := json.Marshal(targonReq)
	if err != nil {
		return nil, errors.Join(errors.New("failed to marshal targon request"), err, shared.ErrInternalServerError)
	}

	url := fmt.Sprintf("%s/v1/inference", t.TargonEndpoint)
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(targonReqJSON))
	if err != nil {
		return nil, errors.Join(errors.New("failed to create http request"), err, shared.ErrInternalServerError)
	}
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.TargonAPIKey))
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := t.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, errors.Join(errors.New("failed to send http request"), err, shared.ErrInternalServerError)
	}
	defer func() {
		if closeErr := res.Body.Close(); closeErr != nil {
			t.Log.Warnw("Failed to close response body", "error", closeErr)
		}
	}()

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, errors.Join(errors.New("failed to read response body"), err, shared.ErrInternalServerError)
	}

	if res.StatusCode != http.StatusOK {
		return nil, errors.Join(fmt.Errorf("targon returned error: [%d: %s]", res.StatusCode, string(resBody)), shared.ErrInternalServerError)
	}

	var targonResp TargonServiceResponse
	if err := json.Unmarshal(resBody, &targonResp); err != nil {
		return nil, errors.Join(errors.New("failed to parse targon response"), err, shared.ErrInternalServerError)
	}

	icpt, ocpt, crc := uint64(100), uint64(200), uint64(50)
	if input.Req.Pricing != nil {
		icpt = input.Req.Pricing.ICPT
		ocpt = input.Req.Pricing.OCPT
		crc = input.Req.Pricing.CRC
	}

	// Marshal supported_endpoints to JSON
	supportedEndpointsJSON, err := json.Marshal(input.Req.SupportedEndpoints)
	if err != nil {
		return nil, errors.Join(errors.New("failed to marshal supported_endpoints"), err, shared.ErrInternalServerError)
	}

	var allowedUserID *uint64
	if input.Req.AllowedUserID > 0 {
		allowedUserID = &input.Req.AllowedUserID
	}

	// Marshal metadata to JSON
	var metadataJSON []byte
	if input.Req.Metadata != nil {
		metadataJSON, err = json.Marshal(input.Req.Metadata)
		if err != nil {
			return nil, errors.Join(errors.New("failed to marshal metadata"), err, shared.ErrInternalServerError)
		}
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
			metadata,
			enabled,
			config,
			targon_uid
		) VALUES (
		 ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	result, err := t.WDB.ExecContext(input.Ctx, insertModelsQuery, input.Req.BaseModel, input.Req.Modality, icpt, ocpt, crc, input.Req.Description, string(supportedEndpointsJSON), allowedUserID, string(metadataJSON), false, string(targonReqJSON), targonResp.UID)
	if err != nil {
		// Try to cleanup the orphaned Targon service
		err = errors.Join(t.cleanupTargonService(targonResp.UID), err)
		return nil, errors.Join(errors.New("failed to insert model into database"), err, shared.ErrInternalServerError)
	}
	modelID, err := result.LastInsertId()
	if err != nil {
		return nil, errors.Join(errors.New("failed to get last insert id"), err, shared.ErrInternalServerError)
	}

	modelNames := input.Req.SupportedModelNames
	modelNames = append(modelNames, input.Req.BaseModel)

	go t.pollAndEnableModel(context.Background(), targonResp.UID, modelNames, uint64(modelID),
		icpt, ocpt, crc, input.Req.Modality, input.Req.AllowedUserID)

	return &CreateModelOutput{
		ModelID:   modelID,
		TargonUID: targonResp.UID,
		Name:      targonResp.Name,
		Status:    "creating",
		Message:   "Model creation initiated. Polling targon for status.",
	}, nil
}

func validateCreateModelRequest(req CreateModelRequest) error {
	if req.BaseModel == "" {
		return errors.New("name is required")
	}
	if req.Framework != "vllm" && req.Framework != "sglang" && req.Framework != "tei" {
		return errors.New("framework must be vllm, sglang, or tei")
	}
	if req.FrameworkVersion == "" {
		return errors.New("framework_version is required")
	}
	if req.ResourceName == "" {
		return errors.New("resource_name is required")
	}
	if req.MaxReplicas < 1 {
		return errors.New("maxReplicas must be at least 1")
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

	// Validate metadata
	if req.Metadata != nil {
		validSamplingParams := map[string]bool{
			"temperature":        true,
			"top_p":              true,
			"top_k":              true,
			"repetition_penalty": true,
			"frequency_penalty":  true,
			"presence_penalty":   true,
			"stop":               true,
			"seed":               true,
		}

		validFeatures := map[string]bool{
			"tools":              true,
			"json_mode":          true,
			"structured_outputs": true,
			"web_search":         true,
			"reasoning":          true,
			"vision_language":    true,
		}

		for _, param := range req.Metadata.SupportedSamplingParameters {
			if !validSamplingParams[param] {
				return fmt.Errorf("invalid sampling parameter: %s. Valid parameters are: temperature, top_p, top_k, repetition_penalty, frequency_penalty, presence_penalty, stop, seed", param)
			}
		}

		for _, feature := range req.Metadata.SupportedFeatures {
			if !validFeatures[feature] {
				return fmt.Errorf("invalid feature: %s. Valid features are: tools, json_mode, structured_outputs, web_search, reasoning, vision_language", feature)
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
	case "tei":
		image = fmt.Sprintf("ghcr.io/huggingface/text-embeddings-inference:%s", version)
		defaultCommand = nil
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
	case "tei":
		port = 80
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

func (t *TargonHandler) pollAndEnableModel(ctx context.Context, targonUID string, modelNames []string, modelID uint64,
	icpt, ocpt, crc uint64, modality string, allowedUserID uint64,
) {
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
