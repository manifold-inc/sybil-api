package inference

import (
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

func (im *InferenceHandler) Preprocess(input PreprocessInput) (*shared.RequestInfo, error) {
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

	if input.Endpoint == shared.ENDPOINTS.EMBEDDING {
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

		if (input.User.Credits == 0 && input.User.PlanRequests == 0) && !input.User.AllowOverspend {
			return nil, &shared.RequestError{
				StatusCode: 402,
				Err:        errors.New("insufficient credits"),
			}
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.Join(&shared.RequestError{StatusCode: 500, Err: errors.New("internal server error")}, err)
		}

		return &shared.RequestInfo{
			Body:      body,
			UserID:    input.User.UserID,
			Credits:   input.User.Credits,
			ID:        input.RequestID,
			StartTime: startTime,
			Endpoint:  input.Endpoint,
			Model:     modelName,
			Stream:    false,
		}, nil
	}

	if input.Endpoint == shared.ENDPOINTS.RESPONSES {
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
	}

	if (input.User.Credits == 0 && input.User.PlanRequests == 0) && !input.User.AllowOverspend {
		return nil, &shared.RequestError{
			StatusCode: 402,
			Err:        errors.New("insufficient requests or credits"),
		}
	}

	// Set stream default if not specified
	if val, ok := payload["stream"]; !ok || val == nil {
		payload["stream"] = shared.DefaultStreamOption
	}

	stream := payload["stream"].(bool)

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

	reqInfo := &shared.RequestInfo{
		Body:      body,
		UserID:    input.User.UserID,
		Credits:   input.User.Credits,
		ID:        input.RequestID,
		StartTime: startTime,
		Endpoint:  input.Endpoint,
		Model:     modelName,
		Stream:    stream,
	}

	return reqInfo, nil
}
