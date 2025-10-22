package inference

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"
	"time"

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
}

func (im *InferenceManager) Models(cc echo.Context) error {
	c := cc.(*setup.Context)

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	c.Log.Infow("Fetching models", "user_id", func() uint64 {
		if c.User != nil {
			return c.User.UserID
		}
		return 0
	}())

	models, err := fetchModels(ctx, im.RDB, c.User, c)
	if err != nil {
		c.Log.Errorw("Failed to get models", "error", err.Error())
		return cc.String(500, "Failed to get models")
	}

	c.Log.Infow("Models fetched successfully", "count", len(models))

	return c.JSON(200, ModelList{
		Data: models,
	})
}

func fetchModels(ctx context.Context, db *sql.DB, user *shared.UserMetadata, c *setup.Context) ([]Model, error) {
	baseQuery := `
		`
	switch true {
	case user != nil:
		c.Log.Infow("Querying user-specific models", "user_id", user.UserID)
		if models, err := queryModels(ctx, db, c, baseQuery+`
				SELECT name, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') as created,
					icpt, ocpt, crc, metadata, modality, supported_endpoints
				FROM model 
				WHERE enabled = true AND allowed_user_id = ?
				ORDER BY name ASC`,
			user.UserID); err == nil && len(models) > 0 {
			c.Log.Infow("Found user-specific models", "count", len(models))
			return models, nil
		} else if err != nil {
			c.Log.Warnw("Error querying user-specific models, falling back to public", "error", err.Error())
		} else {
			c.Log.Infow("No user-specific models found, falling back to public")
		}
		fallthrough
	default:
		// public models
		c.Log.Infow("Querying public models")
		return queryModels(ctx, db, c, `
			SELECT name, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') as created,
				icpt, ocpt, crc, metadata, modality, supported_endpoints
			FROM model 
			WHERE enabled = true AND allowed_user_id is NULL
			ORDER BY name ASC`)
	}
}

func queryModels(ctx context.Context, db *sql.DB, c *setup.Context, query string, args ...any) ([]Model, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []Model
	rowCount := 0
	for rows.Next() {
		rowCount++
		model, err := scanModel(rows)
		if err != nil {
			c.Log.Warnw("Failed to scan model row", "row", rowCount, "error", err.Error())
			continue
		}
		models = append(models, model)
	}

	if err := rows.Err(); err != nil {
		c.Log.Errorw("Error iterating over rows", "error", err.Error())
		return nil, err
	}

	c.Log.Infow("Query complete", "rows_scanned", rowCount, "models_added", len(models))
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

	// Handle metadata gracefully - use empty defaults if NULL
	var metadata ModelMetadata
	if metadataJSON.Valid && metadataJSON.String != "" {
		_ = json.Unmarshal([]byte(metadataJSON.String), &metadata)
	}

	var supportedEndpoints []string
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
	default:
		// Default to text if modality is not recognized
		model.InputModalities = []string{"text"}
		model.OutputModalities = []string{"text"}
	}

	// Use metadata if available, otherwise use defaults
	if metadata.Name != "" {
		model.Name = metadata.Name
	} else {
		model.Name = name // Fallback to the model ID
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

	model.SupportedSamplingParameters = metadata.SupportedSamplingParameters
	model.SupportedFeatures = metadata.SupportedFeatures

	return model, nil
}
