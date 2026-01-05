package inference

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"sybil-api/internal/shared"
)

type PreprocessInput struct {
	Body      []byte
	User      shared.UserMetadata
	Endpoint  string
	RequestID string
	LogFields map[string]string
}

func (im *InferenceHandler) Preprocess(input PreprocessInput) (*shared.RequestInfo, *shared.RequestError) {
	startTime := time.Now()

	newlog := logWithFields(im.Log, input.LogFields)

	// Unmarshal to generic map to set defaults
	var payload map[string]any
	err := json.Unmarshal(input.Body, &payload)
	if err != nil {
		newlog.Warnw("failed json unmarshal to payload map", "error", err.Error())
		return nil, &shared.RequestError{StatusCode: 400, Err: errors.New("malformed request")}
	}

	// validate models and set defaults
	model, ok := payload["model"]
	if !ok {
		newlog.Infow("missing model parameter", "error", "model is required")
		return nil, &shared.RequestError{StatusCode: 400, Err: errors.New("model is required")}
	}

	modelName := model.(string)

	newlog = newlog.With("model", modelName, "endpoint", input.Endpoint)

	if input.Endpoint == shared.ENDPOINTS.GENERATION {
		prompt, ok := payload["prompt"]
		if !ok {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("prompt is required for image generation"),
			}
		}

		promptStr, ok := prompt.(string)
		if !ok || strings.TrimSpace(promptStr) == "" {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("prompt must be a non-empty string"),
			}
		}

		if responseFormat, ok := payload["response_format"]; ok && responseFormat != nil {
			formatStr, ok := responseFormat.(string)
			if !ok {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("response_format must be a string"),
				}
			}
			switch formatStr {
			case "url", "b64_json":
			default:
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("response_format must be 'url' or 'b64_json'"),
				}
			}
		}

		if nValue, ok := payload["n"]; ok && nValue != nil {
			nFloat, ok := nValue.(float64)
			if !ok {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("n must be a number"),
				}
			}

			nInt := int(nFloat)
			if nInt < 1 || nInt > 10 {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("n must be between 1 and 10"),
				}
			}

			payload["n"] = nInt
		}

		payload["prompt"] = promptStr
		payload["stream"] = false
	}

	if input.Endpoint == shared.ENDPOINTS.EDITS {
		prompt, ok := payload["prompt"]
		if !ok {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("prompt is required for image edits"),
			}
		}

		promptStr, ok := prompt.(string)
		if !ok || strings.TrimSpace(promptStr) == "" {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("prompt must be a non-empty string"),
			}
		}

		imageField, ok := payload["image"]
		if !ok {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("image is required for image edits"),
			}
		}

		imageStr, ok := imageField.(string)
		if !ok || strings.TrimSpace(imageStr) == "" {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("image must be a non-empty base64 png string"),
			}
		}

		if maskField, ok := payload["mask"]; ok && maskField != nil {
			maskStr, ok := maskField.(string)
			if !ok || strings.TrimSpace(maskStr) == "" {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("mask must be a non-empty base64 png string"),
				}
			}
			payload["mask"] = maskStr
		}

		if responseFormat, ok := payload["response_format"]; ok && responseFormat != nil {
			formatStr, ok := responseFormat.(string)
			if !ok {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("response_format must be a string"),
				}
			}
			switch formatStr {
			case "url", "b64_json":
			default:
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("response_format must be 'url' or 'b64_json'"),
				}
			}
		}

		if nValue, ok := payload["n"]; ok && nValue != nil {
			nFloat, ok := nValue.(float64)
			if !ok {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("n must be a number"),
				}
			}

			nInt := int(nFloat)
			if nInt < 1 || nInt > 10 {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("n must be between 1 and 10"),
				}
			}

			payload["n"] = nInt
		}

		payload["prompt"] = promptStr
		payload["image"] = imageStr
		payload["stream"] = false
	}

	if input.Endpoint == shared.ENDPOINTS.VARIATIONS {
		imageField, ok := payload["image"]
		if !ok {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("image is required for image variations"),
			}
		}

		imageStr, ok := imageField.(string)
		if !ok || strings.TrimSpace(imageStr) == "" {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("image must be a non-empty base64 png string"),
			}
		}

		if responseFormat, ok := payload["response_format"]; ok && responseFormat != nil {
			formatStr, ok := responseFormat.(string)
			if !ok {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("response_format must be a string"),
				}
			}
			switch formatStr {
			case "url", "b64_json":
			default:
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("response_format must be 'url' or 'b64_json'"),
				}
			}
		}

		if nValue, ok := payload["n"]; ok && nValue != nil {
			nFloat, ok := nValue.(float64)
			if !ok {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("n must be a number"),
				}
			}

			nInt := int(nFloat)
			if nInt < 1 || nInt > 10 {
				return nil, &shared.RequestError{
					StatusCode: 400,
					Err:        errors.New("n must be between 1 and 10"),
				}
			}

			payload["n"] = nInt
		}

		payload["image"] = imageStr
		payload["stream"] = false
	}

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
			newlog.Infow("No credits available", "user_id", input.User.UserID)
			return nil, &shared.RequestError{
				StatusCode: 402,
				Err:        errors.New("insufficient credits"),
			}
		}

		body, err := json.Marshal(payload)
		if err != nil {
			newlog.Errorw("Failed to marshal request body", "error", err.Error())
			return nil, &shared.RequestError{StatusCode: 500, Err: errors.New("internal server error")}
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
		newlog.Warnw("Insufficient credits or requests",
			"credits", input.User.Credits,
			"plan_requests", input.User.PlanRequests,
			"allow_overspend", input.User.AllowOverspend)
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

	// Add stream to logger context
	newlog = newlog.With("stream", stream)

	// If streaming is enabled (either by default or explicitly), include usage data
	if stream {
		payload["stream_options"] = map[string]any{
			"include_usage": true,
		}
	}

	// Log user id 3's request parameters
	if input.User.UserID == 3 {
		newlog.Infow("User 3 request payload",
			"model", modelName,
			"stream", stream,
			"max_tokens", payload["max_tokens"],
			"temperature", payload["temperature"],
			"top_p", payload["top_p"],
			"frequency_penalty", payload["frequency_penalty"],
			"presence_penalty", payload["presence_penalty"])
	}

	// repackage body
	body, err := json.Marshal(payload)
	if err != nil {
		newlog.Errorw("Failed to marshal request body", "error", err.Error())
		return nil, &shared.RequestError{StatusCode: 500, Err: errors.New("internal server error")}
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
