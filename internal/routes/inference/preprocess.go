package inference

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

func (im *InferenceManager) ChatRequest(c echo.Context) error {
	_, err := im.ProcessOpenaiRequest(c, shared.ENDPOINTS.CHAT)
	return err
}

func (im *InferenceManager) CompletionRequest(c echo.Context) error {
	_, err := im.ProcessOpenaiRequest(c, shared.ENDPOINTS.COMPLETION)
	return err
}

func (im *InferenceManager) CompletionRequestHistory(c echo.Context) (string, error) {
	return im.ProcessOpenaiRequest(c, shared.ENDPOINTS.CHAT)
}

func (im *InferenceManager) EmbeddingRequest(c echo.Context) error {
	_, err := im.ProcessOpenaiRequest(c, shared.ENDPOINTS.EMBEDDING)
	return err
}

func (im *InferenceManager) ResponsesRequest(c echo.Context) error {
	_, err := im.ProcessOpenaiRequest(c, shared.ENDPOINTS.RESPONSES)
	return err
}

func (im *InferenceManager) preprocessOpenAIRequest(
	c *setup.Context,
	endpoint string,
) (*shared.RequestInfo, *shared.RequestError) {
	startTime := time.Now()

	userInfo := c.User

	// Ensure properly formatted request
	body, _ := io.ReadAll(c.Request().Body)

	// Unmarshal to generic map to set defaults
	var payload map[string]any
	err := json.Unmarshal(body, &payload)
	if err != nil {
		c.Log.Warnw("failed json unmarshal to payload map", "error", err.Error())
		return nil, &shared.RequestError{StatusCode: 400, Err: errors.New("malformed request")}
	}

	// validate models and set defaults
	model, ok := payload["model"]
	if !ok {
		c.Log.Infow("missing model parameter", "error", "model is required")
		return nil, &shared.RequestError{StatusCode: 400, Err: errors.New("model is required")}
	}

	modelName := model.(string)

	// Add model and endpoint to logger context for all subsequent logs
	c.Log = c.Log.With("model", modelName, "endpoint", endpoint)

	if endpoint == shared.ENDPOINTS.EMBEDDING {

		input, ok := payload["input"]
		if !ok {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("input is required for embeddings"),
			}
		}

		switch v := input.(type) {
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

		if (userInfo.Credits == 0 && userInfo.PlanRequests == 0) && !userInfo.AllowOverspend {
			c.Log.Infow("No credits available", "user_id", userInfo.UserID)
			return nil, &shared.RequestError{
				StatusCode: 402,
				Err:        errors.New("insufficient credits"),
			}
		}

		body, err = json.Marshal(payload)
		if err != nil {
			c.Log.Errorw("Failed to marshal request body", "error", err.Error())
			return nil, &shared.RequestError{StatusCode: 500, Err: errors.New("internal server error")}
		}

		return &shared.RequestInfo{
			Body:      body,
			UserID:    userInfo.UserID,
			Credits:   userInfo.Credits,
			ID:        c.Reqid,
			StartTime: startTime,
			Endpoint:  endpoint,
			Model:     modelName,
			Stream:    false,
		}, nil
	}

	if endpoint == shared.ENDPOINTS.RESPONSES {
		input, ok := payload["input"]
		if !ok {
			return nil, &shared.RequestError{
				StatusCode: 400,
				Err:        errors.New("input is required for responses"),
			}
		}

		inputArray, ok := input.([]any)
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

	if (userInfo.Credits == 0 && userInfo.PlanRequests == 0) && !userInfo.AllowOverspend {
		c.Log.Warnw("Insufficient credits or requests",
			"credits", userInfo.Credits,
			"plan_requests", userInfo.PlanRequests,
			"allow_overspend", userInfo.AllowOverspend)
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
	c.Log = c.Log.With("stream", stream)

	// If streaming is enabled (either by default or explicitly), include usage data
	if stream {
		payload["stream_options"] = map[string]any{
			"include_usage": true,
		}
	}

	// Log user id 3's request parameters
	if userInfo.UserID == 3 {
		c.Log.Infow("User 3 request payload",
			"model", modelName,
			"stream", stream,
			"max_tokens", payload["max_tokens"],
			"temperature", payload["temperature"],
			"top_p", payload["top_p"],
			"frequency_penalty", payload["frequency_penalty"],
			"presence_penalty", payload["presence_penalty"])
	}

	// if user id is 2, and deepseek v3-0324, set max tokens to 4096 if not provided
	if userInfo.UserID == 2 && modelName == "deepseek/DeepSeek-V3-0324" {
		if _, ok := payload["max_tokens"]; !ok {
			payload["max_tokens"] = 4096
		}
	}

	// repackage body
	body, err = json.Marshal(payload)
	if err != nil {
		c.Log.Errorw("Failed to marshal request body", "error", err.Error())
		return nil, &shared.RequestError{StatusCode: 500, Err: errors.New("internal server error")}
	}

	reqInfo := &shared.RequestInfo{
		Body:      body,
		UserID:    userInfo.UserID,
		Credits:   userInfo.Credits,
		ID:        c.Reqid,
		StartTime: startTime,
		Endpoint:  endpoint,
		Model:     modelName,
		Stream:    stream,
	}

	return reqInfo, nil
}

func (im *InferenceManager) ProcessOpenaiRequest(cc echo.Context, endpoint string) (string, error) {
	c := cc.(*setup.Context)

	// Add endpoint to logger context
	c.Log = c.Log.With("endpoint", endpoint)

	reqInfo, preprocessError := im.preprocessOpenAIRequest(c, endpoint)
	if preprocessError != nil {
		if preprocessError.StatusCode >= 500 {
			c.Log.Warnw("Preprocess error", "error", preprocessError.Err.Error())
		}
		return "", c.String(preprocessError.StatusCode, preprocessError.Error())
	}

	im.usageCache.AddInFlightToBucket(reqInfo.UserID)

	// ensure we remove inflight BEFORE we add this to a bucket
	mu := sync.Mutex{}
	mu.Lock()
	defer func() {
		im.usageCache.RemoveInFlightFromBucket(reqInfo.UserID)
		mu.Unlock()
	}()

	resInfo, qerr := im.QueryModels(c, reqInfo)
	if qerr != nil {
		c.Log.Warnw("QueryModels failed",
			"error", qerr.Error(),
			"status_code", qerr.StatusCode)

		/* TODO: Revisit overload logic
		if qerr.StatusCode == 502 {
			overload.TrackTPS(
				c.Core,
				c.ModelDNS,
				1,
			)
		} */

		return "", c.JSON(qerr.StatusCode, shared.OpenAIError{
			Message: qerr.Error(),
			Object:  "error",
			Type:    "InternalError",
			Code:    qerr.StatusCode,
		})
	}

	// Extract usage data from the response content
	if resInfo.ResponseContent == "" || !resInfo.Completed {
		c.Log.Errorw("No response or incomplete response from model",
			"response_content_length", len(resInfo.ResponseContent),
			"completed", resInfo.Completed,
			"canceled", resInfo.Canceled,
			"ttft", resInfo.TimeToFirstToken,
			"total_time", resInfo.TotalTime)
		_ = c.JSON(500, shared.OpenAIError{
			Message: "no response from model",
			Object:  "error",
			Type:    "InternalError",
			Code:    500,
		})
	}

	// Asynchronously process request and return to the user
	log := c.Log
	go func() {
		switch true {
		case !resInfo.Completed:
			break
		case reqInfo.Stream:
			var chunks []map[string]any
			err := json.Unmarshal([]byte(resInfo.ResponseContent), &chunks)
			if err != nil {
				log.Errorw(
					"Failed to unmarshal streaming ResponseContent as JSON array of chunks",
					"error",
					err,
					"raw_response_content",
					resInfo.ResponseContent,
				)
				break
			}
			slices.Reverse(chunks)
			for i, chunk := range chunks {
				usageData, usageFieldExists := chunk["usage"]
				if usageFieldExists && usageData != nil {
					if extractedUsage, extractErr := extractUsageData(chunk, endpoint); extractErr == nil {
						resInfo.Usage = extractedUsage
						break
					}
					log.Warnw(
						"Failed to extract usage data from a response chunk that had a non-null usage field",
						"chunk_index",
						i,
					)
					break
				}
			}
		case !reqInfo.Stream:
			// Not a streaming request, expect a single JSON object
			var singleResponse map[string]any
			err := json.Unmarshal([]byte(resInfo.ResponseContent), &singleResponse)
			if err != nil {
				log.Errorw(
					"Failed to unmarshal non-streaming ResponseContent as single JSON object",
					"error",
					err,
					"raw_response_content",
					resInfo.ResponseContent,
				)
				break
			}
			usageData, usageFieldExists := singleResponse["usage"]
			if usageFieldExists && usageData != nil {
				if extractedUsage, extractErr := extractUsageData(singleResponse, endpoint); extractErr == nil {
					resInfo.Usage = extractedUsage
					break
				}
				log.Warnw(
					"Failed to extract usage data from single response object that had a non-null usage field",
				)
			}
		default:
			break
		}

		// Ensure resInfo.Usage is not nil before saving (this is a good fallback)
		if resInfo.Usage == nil {
			resInfo.Usage = &shared.Usage{IsCanceled: resInfo.Canceled}
		}

		totalCredits := shared.CalculateCredits(resInfo.Usage, resInfo.Cost.InputCredits, resInfo.Cost.OutputCredits, resInfo.Cost.CanceledCredits)

		pqi := &shared.ProcessedQueryInfo{
			UserID:           reqInfo.UserID,
			Model:            reqInfo.Model,
			ModelID:          resInfo.ModelID,
			Endpoint:         reqInfo.Endpoint,
			TotalTime:        resInfo.TotalTime,
			TimeToFirstToken: resInfo.TimeToFirstToken,
			Usage:            resInfo.Usage,
			Cost:             resInfo.Cost,
			TotalCredits:     totalCredits,
			ResponseContent:  resInfo.ResponseContent,
			RequestContent:   reqInfo.Body,
			CreatedAt:        time.Now(),
			ID:               reqInfo.ID,
		}

		/* TODO: ditto
		if resInfo.Completed {
			overload.TrackTPS(
				core,
				modelDNS,
				float64(resInfo.Usage.CompletionTokens)/resInfo.TotalTime.Seconds(),
			)
		}
		*/
		mu.Lock()
		im.usageCache.AddRequestToBucket(reqInfo.UserID, pqi, reqInfo.ID)
		mu.Unlock()
	}()

	return resInfo.ResponseContent, nil
}

// Helper function to safely extract float64 values from a map
func getTokenCount(usageData map[string]any, field string) (uint64, error) {
	value, ok := usageData[field]
	if !ok {
		return 0, fmt.Errorf("missing %s field", field)
	}
	floatVal, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("invalid type for %s field", field)
	}
	return uint64(floatVal), nil
}

// Helper function to safely extract usage data from response
func extractUsageData(response map[string]any, endpoint string) (*shared.Usage, error) {
	usageData, ok := response["usage"].(map[string]any)
	if !ok {
		return nil, errors.New("missing or invalid usage data")
	}

	var promptTokens, completionTokens, totalTokens uint64
	var err error

	// Handle Responses API format (input_tokens, output_tokens)
	if endpoint == shared.ENDPOINTS.RESPONSES {
		promptTokens, err = getTokenCount(usageData, "input_tokens")
		if err != nil {
			return nil, fmt.Errorf("error getting input tokens: %w", err)
		}

		completionTokens, err = getTokenCount(usageData, "output_tokens")
		if err != nil {
			return nil, fmt.Errorf("error getting output tokens: %w", err)
		}

		totalTokens = promptTokens + completionTokens
	} else {
		// Handle Chat/Completions format (prompt_tokens, completion_tokens)
		promptTokens, err = getTokenCount(usageData, "prompt_tokens")
		if err != nil {
			return nil, fmt.Errorf("error getting prompt tokens: %w", err)
		}

		completionTokens = uint64(0)
		if endpoint != shared.ENDPOINTS.EMBEDDING {
			completionTokens, err = getTokenCount(usageData, "completion_tokens")
			if err != nil {
				return nil, fmt.Errorf("error getting completion tokens: %w", err)
			}
		}

		totalTokens, err = getTokenCount(usageData, "total_tokens")
		if err != nil {
			return nil, fmt.Errorf("error getting total tokens: %w", err)
		}
	}

	return &shared.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		IsCanceled:       false,
	}, nil
}
