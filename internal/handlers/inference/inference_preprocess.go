package inference

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"sybil-api/internal/shared"
)

type PreprocessInput struct {
	Body      []byte
	User      shared.UserMetadata
	Endpoint  string
	RequestID string
}

type RequestInfo struct {
	Body          []byte
	UserID        uint64
	Credits       uint64
	ID            string
	StartTime     time.Time
	Endpoint      string
	Model         string
	Stream        bool
	URL           string
	ModelMetadata *InferenceService
}

func (im *InferenceHandler) Preprocess(ctx context.Context, input PreprocessInput) (*RequestInfo, error) {
	startTime := time.Now()

	// Unmarshal to generic map to set defaults
	var payload map[string]any
	err := json.Unmarshal(input.Body, &payload)
	if err != nil {
		return nil, errors.Join(shared.ErrBadRequest, err)
	}

	// validate models and set defaults
	model, ok := payload["model"]
	if !ok {
		return nil, &shared.RequestError{StatusCode: 400, Err: errors.New("model is required")}
	}

	modelName := model.(string)
	stream := false

	switch input.Endpoint {
	case shared.ENDPOINTS.EMBEDDING:
		inputField, ok := payload["input"]
		if !ok {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("input is required for embeddings"),
			}
		}

		switch v := inputField.(type) {
		case string:
			if v == "" {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("input cannot be empty"),
				}
			}
		case []any:
			if len(v) == 0 {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("input array cannot be empty"),
				}
			}
		default:
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("input must be string or array of strings"),
			}
		}
	case shared.ENDPOINTS.RESPONSES:

		inputField, ok := payload["input"]
		if !ok {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("input is required for responses"),
			}
		}

		inputArray, ok := inputField.([]any)
		if !ok {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("input must be an array"),
			}
		}

		if len(inputArray) == 0 {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("input array cannot be empty"),
			}
		}

		// Fallthrough to set stream settings
		fallthrough
	case shared.ENDPOINTS.CHAT, shared.ENDPOINTS.COMPLETION:
		// Set stream default if not specified
		if val, ok := payload["stream"]; !ok || val == nil {
			payload["stream"] = shared.DefaultStreamOption
		}
		stream = payload["stream"].(bool)
	}

	if (input.User.Credits == 0 && input.User.PlanRequests == 0) && !input.User.AllowOverspend {
		return nil, &shared.RequestError{
			StatusCode: 402,
			Err:        errors.New("insufficient requests or credits"),
		}
	}

	// If streaming is enabled (either by default or explicitly), include usage data
	if stream {
		payload["stream_options"] = map[string]any{
			"include_usage": true,
		}
	}

	// repackage body
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Join(&shared.RequestError{StatusCode: 500, Err: errors.New("internal server error")}, err)
	}

	modelMetadata, err := im.DiscoverModels(ctx, input.User.UserID, modelName)
	if err != nil {
		return nil, errors.Join(&shared.RequestError{
			StatusCode: 404,
			Err:        errors.New("model not found"),
		}, err)
	}

	reqInfo := &RequestInfo{
		Body:          body,
		UserID:        input.User.UserID,
		Credits:       input.User.Credits,
		ID:            input.RequestID,
		StartTime:     startTime,
		Endpoint:      input.Endpoint,
		Model:         modelName,
		Stream:        stream,
		ModelMetadata: modelMetadata,
	}

	return reqInfo, nil
}
