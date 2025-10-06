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
	Private             bool     `json:"private,omitempty"`
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

	ContainerConcurrency *int64 `json:"containerConcurrency,omitempty"`
	TimeoutSeconds       *int64 `json:"timeoutSeconds,omitempty"`
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
	Name       string         `json:"name"`
	Image      string         `json:"image"`
	Command    []string       `json:"command,omitempty"`
	Args       []string       `json:"args,omitempty"`
	WorkingDir string         `json:"workingDir,omitempty"`
	Ports      []TargonPort   `json:"ports,omitempty"`
	Env        []TargonEnvVar `json:"env,omitempty"`
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
	UID    string `json:"uid"`
	Status *struct {
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
	defer res.Body.Close()

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

	var allowedUserID *uint64
	if req.Private && req.AllowedUserID > 0 {
		allowedUserID = &req.AllowedUserID
	}

	// Marshal supported_endpoints to JSON
	supportedEndpointsJSON, err := json.Marshal(req.SupportedEndpoints)
	if err != nil {
		t.Log.Errorw("Failed to marshal supported_endpoints", "error", err)
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	insertModelsQuery := `
		INSERT INTO models (
			name, 
			modality,
			icpt,
			ocpt,
			crc,
			description,
			supported_endpoints,
			allowed_user_id,
			private,
			enabled,
			config
		) VALUES (
		 ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	result, err := t.WDB.ExecContext(c.Request().Context(), insertModelsQuery, req.BaseModel, req.Modality, icpt, ocpt, crc, req.Description, supportedEndpointsJSON, allowedUserID, req.Private, false, targonReqJSON)
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

	// Pass model metadata to polling function (avoid unnecessary DB query)
	go t.pollAndEnableModel(c.Request().Context(), targonResp.UID, req.SupportedModelNames, uint64(modelID),
		icpt, ocpt, crc, req.Private, req.Modality, allowedUserID)

	return c.JSON(http.StatusOK, map[string]any{
		"model_id":   modelID,
		"targon_uid": targonResp.UID,
		"name":       targonResp.Name,
		"status":     "creating",
		"message":    fmt.Sprintf("Model creation initiated. Polling targon GET /models/%s for status", targonResp.UID),
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
	switch strings.ToLower(req.Framework) {
	case "vllm":
		image = fmt.Sprintf("vllm/vllm-openai:%s", version)
	case "sglang":
		image = fmt.Sprintf("lmsysorg/sglang:%s", version)
	default:
		image = fmt.Sprintf("vllm/vllm-openai:%s", version)
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
	sybilID, err := nanoid.Generate(nanoid.DefaultAlphabet, 10)
	if err != nil {
		return TargonCreateRequest{}, errors.New("failed to generate nanoid")
	}
	sybilName := fmt.Sprintf("sybil-%s", sybilID)

	return TargonCreateRequest{
		Name:         sybilName,
		ResourceName: req.ResourceName,
		Framework:    req.Framework,
		Predictor: TargonPredictorConfig{
			Container: TargonCustomInferenceContainer{
				Name:  sybilName,
				Image: image,
				Args:  req.Args,
				Env:   envVars,
				Ports: []TargonPort{{ContainerPort: port, Protocol: "TCP"}},
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
	icpt, ocpt, crc uint64, private bool, modality string, allowedUserID *uint64) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	maxAttempts := 120 // 120 * 30 = 60 minutes
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
			res.Body.Close() // Close immediately
			if err != nil {
				t.Log.Errorw("Failed to read response body", "error", err.Error())
				continue
			}

			var targonResp TargonServiceStatusResponse
			if err := json.Unmarshal(resBody, &targonResp); err != nil {
				t.Log.Errorw("Failed to parse Targon response", "error", err.Error())
				continue
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

					// Cache full model details
					cacheKey := fmt.Sprintf("model:service:%s", modelName)
					serviceCache := map[string]interface{}{
						"model_id":        modelID,
						"url":             targonResp.Status.URL,
						"icpt":            icpt,
						"ocpt":            ocpt,
						"crc":             crc,
						"private":         private,
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

					if err := t.RedisClient.Set(ctx, cacheKey, cacheJSON, 30*time.Minute).Err(); err != nil {
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
				_, updateErr := t.WDB.ExecContext(ctx, "UPDATE models SET enabled = true WHERE id = ?", modelID)
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

// clean up orphaned Targon service if anything goes wrong
func (t *TargonManager) cleanupTargonService(targonUID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	defer res.Body.Close()

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
