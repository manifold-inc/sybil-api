package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/manifold-inc/manifold-sdk/lib/utils"
	"sybil-api/internal/shared"
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

func (im *InferenceHandler) ListModels(ctx context.Context, userID *uint64) ([]Model, error) {
	if userID != nil {
		userModels, _ := im.queryModels(ctx, `
			SELECT name, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') as created,
				icpt, ocpt, crc, metadata, modality, supported_endpoints
			FROM model 
			WHERE enabled = true AND allowed_user_id = ?
			ORDER BY name ASC`, *userID)

		if len(userModels) > 0 {
			return userModels, nil
		}
	}

	return im.queryModels(ctx, `
		SELECT name, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') as created,
			icpt, ocpt, crc, metadata, modality, supported_endpoints
		FROM model 
		WHERE enabled = true AND allowed_user_id is NULL
		ORDER BY name ASC`)
}

func (im *InferenceHandler) queryModels(ctx context.Context, query string, args ...any) ([]Model, error) {
	rows, err := im.RDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var models []Model
	for rows.Next() {
		model, err := scanModel(rows)
		if err != nil {
			im.Log.Warnw("Failed to scan model row", "error", err.Error(), "args", fmt.Sprintf("%v", args))
			continue
		}
		models = append(models, model)
	}

	if err := rows.Err(); err != nil {
		return nil, utils.Wrap("Error iterating over queryModels rows", err)
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
