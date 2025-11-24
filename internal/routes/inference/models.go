package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

type Model struct {
	ID                          string   `json:"id"`
	Name                        string   `json:"name"`
	Created                     int64    `json:"created"`
	InputModalities             []string `json:"input_modalities"`
	OutputModalities            []string `json:"output_modalities"`
	Quantization                string   `json:"quantization"`
	ContextLength               int      `json:"context_length"`
	MaxOutputLength             int      `json:"max_output_length"`
	Pricing                     Pricing  `json:"pricing"`
	SupportedSamplingParameters []string `json:"supported_sampling_parameters"`
	SupportedFeatures           []string `json:"supported_features"`
	SupportedEndpoints          []string `json:"supported_endpoints"`
	OutputDimensions            *int     `json:"output_dimensions,omitempty"`
	MaxBatchSize                *int     `json:"max_batch_size,omitempty"`
	Normalized                  *bool    `json:"normalized,omitempty"`
	EmbeddingType               string   `json:"embedding_type,omitempty"`
	MaxInputLength              *int     `json:"max_input_length,omitempty"`
}

type Pricing struct {
	Prompt           string  `json:"prompt"`
	Completion       string  `json:"completion"`
	Image            string  `json:"image"`
	CancelledRequest *string `json:"cancelled_request,omitempty"`
	Request          string  `json:"request"`
	InputCacheReads  string  `json:"input_cache_reads"`
	InputCacheWrites string  `json:"input_cache_writes"`
}

type ModelList struct {
	Data []Model `json:"data"`
}

type ModelMetadata struct {
	Name                        string   `json:"name"`
	Quantization                string   `json:"quantization"`
	ContextLength               int      `json:"context_length"`
	MaxOutputLength             int      `json:"max_output_length"`
	SupportedSamplingParameters []string `json:"supported_sampling_parameters"`
	SupportedFeatures           []string `json:"supported_features"`
	InputModalities             []string `json:"input_modalities"`
	OutputModalities            []string `json:"output_modalities"`
	OutputDimensions            *int     `json:"output_dimensions,omitempty"`
	MaxBatchSize                *int     `json:"max_batch_size,omitempty"`
	Normalized                  *bool    `json:"normalized,omitempty"`
	EmbeddingType               string   `json:"embedding_type,omitempty"`
	MaxInputLength              *int     `json:"max_input_length,omitempty"`
}

func (im *InferenceManager) Models(cc echo.Context) error {
	c := cc.(*setup.Context)

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	// Build log fields for structured logging
	logfields := map[string]string{
		"endpoint": "models",
	}
	if c.User != nil {
		logfields["user_id"] = fmt.Sprintf("%d", c.User.UserID)
	}

	// Call business logic with explicit parameters
	var userID *uint64
	if c.User != nil {
		userID = &c.User.UserID
	}

	models, err := im.fetchModels(ctx, userID, logfields)
	if err != nil {
		c.Log.Errorw("Failed to get models", "error", err.Error())
		return cc.String(500, "Failed to get models")
	}

	return c.JSON(200, ModelList{
		Data: models,
	})
}

// fetchModels retrieves models from the database based on user permissions
// It first tries to fetch user-specific models, then falls back to public models
func (im *InferenceManager) fetchModels(ctx context.Context, userID *uint64, logfields map[string]string) ([]Model, error) {
	// Build logger with structured fields
	log := im.Log
	for k, v := range logfields {
		log = log.With(k, v)
	}

	// Try to fetch user-specific models first if user is authenticated
	if userID != nil {
		userModels, err := im.queryModels(ctx, logfields, `
			SELECT name, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') as created,
				icpt, ocpt, crc, metadata, modality, supported_endpoints
			FROM model 
			WHERE enabled = true AND allowed_user_id = ?
			ORDER BY name ASC`, *userID)

		if err != nil {
			log.Warnw("Error querying user-specific models, falling back to public", "error", err.Error())
		} else if len(userModels) > 0 {
			return userModels, nil
		}
	}

	// Fetch public models (fallback or default)
	return im.queryModels(ctx, logfields, `
		SELECT name, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') as created,
			icpt, ocpt, crc, metadata, modality, supported_endpoints
		FROM model 
		WHERE enabled = true AND allowed_user_id is NULL
		ORDER BY name ASC`)
}

// queryModels executes a database query and scans the results into Model structs
func (im *InferenceManager) queryModels(ctx context.Context, logfields map[string]string, query string, args ...any) ([]Model, error) {
	// Build logger with structured fields
	log := im.Log
	for k, v := range logfields {
		log = log.With(k, v)
	}

	rows, err := im.RDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []Model
	for rows.Next() {
		model, err := scanModel(rows)
		if err != nil {
			log.Warnw("Failed to scan model row", "error", err.Error())
			continue
		}
		models = append(models, model)
	}

	if err := rows.Err(); err != nil {
		log.Errorw("Error iterating over rows", "error", err.Error())
		return nil, err
	}

	return models, nil
}

func scanModel(rows *sql.Rows) (Model, error) {
	var model Model
	var createdAtStr string
	var name string
	var icpt uint64
	var ocpt uint64
	var crc uint64
	var metadataJSON sql.NullString
	var modality string
	var supportedEndpointsJSON sql.NullString

	if err := rows.Scan(&name, &createdAtStr, &icpt, &ocpt, &crc, &metadataJSON, &modality, &supportedEndpointsJSON); err != nil {
		return Model{}, err
	}

	createdAt, err := time.Parse("2006-01-02 15:04:05", createdAtStr)
	if err != nil {
		return Model{}, err
	}

	var metadata ModelMetadata
	if metadataJSON.Valid && metadataJSON.String != "" {
		_ = json.Unmarshal([]byte(metadataJSON.String), &metadata)
	}

	supportedEndpoints := []string{}
	if supportedEndpointsJSON.Valid && supportedEndpointsJSON.String != "" {
		_ = json.Unmarshal([]byte(supportedEndpointsJSON.String), &supportedEndpoints)
	}

	model.ID = name
	model.Created = createdAt.Unix()
	model.SupportedEndpoints = supportedEndpoints

	switch modality {
	case "text-to-text":
		model.InputModalities = []string{"text"}
		model.OutputModalities = []string{"text"}
	case "text-to-image":
		model.InputModalities = []string{"text"}
		model.OutputModalities = []string{"image"}
	case "text-to-embedding":
		model.InputModalities = []string{"text"}
		model.OutputModalities = []string{"embedding"}
	default:
		// Default to text if modality is not recognized
		model.InputModalities = []string{"text"}
		model.OutputModalities = []string{"text"}
	}

	// Use metadata if available, otherwise use defaults
	if metadata.Name != "" {
		model.Name = metadata.Name
	} else {
		model.Name = name
	}

	model.Quantization = metadata.Quantization
	model.ContextLength = metadata.ContextLength
	model.MaxOutputLength = metadata.MaxOutputLength

	promptUSD := float64(icpt) * shared.CreditsToUSD
	completionUSD := float64(ocpt) * shared.CreditsToUSD
	cancelledUSD := float64(crc) * shared.CreditsToUSD

	pricing := Pricing{
		Prompt:           fmt.Sprintf("%.8f", promptUSD),
		Completion:       fmt.Sprintf("%.8f", completionUSD),
		Image:            "0",
		Request:          "0",
		InputCacheReads:  "0",
		InputCacheWrites: "0",
	}

	if crc > 0 {
		cancelledRequestStr := fmt.Sprintf("%.8f", cancelledUSD)
		pricing.CancelledRequest = &cancelledRequestStr
	}

	model.Pricing = pricing

	model.SupportedSamplingParameters = []string{}
	model.SupportedFeatures = []string{}
	if metadata.SupportedSamplingParameters != nil {
		model.SupportedSamplingParameters = metadata.SupportedSamplingParameters
	}
	if metadata.SupportedFeatures != nil {
		model.SupportedFeatures = metadata.SupportedFeatures
	}

	// Embedding-specific metadata
	model.OutputDimensions = metadata.OutputDimensions
	model.MaxBatchSize = metadata.MaxBatchSize
	model.Normalized = metadata.Normalized
	model.EmbeddingType = metadata.EmbeddingType
	model.MaxInputLength = metadata.MaxInputLength

	return model, nil
}
