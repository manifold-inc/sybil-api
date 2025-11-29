package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"sybil-api/internal/metrics"
	"sybil-api/internal/shared"
)

type QueryInput struct {
	Ctx          context.Context
	Req          *shared.RequestInfo
	LogFields    map[string]string
	StreamWriter func(token string) error // Optional callback for real-time streaming
}

// QueryModels forwards the request to the appropriate model
func (im *InferenceHandler) QueryModels(input QueryInput) (*shared.ResponseInfo, *shared.RequestError) {
	newlog := logWithFields(im.Log, input.LogFields)

	// Discover inference service
	modelMetadata, err := im.DiscoverModels(input.Ctx, input.Req.UserID, input.Req.Model)
	if err != nil {
		newlog.Errorw("Service discovery failed", "error", err)
		return nil, &shared.RequestError{
			StatusCode: 404,
			Err:        fmt.Errorf("service not found: %w", err),
		}
	}

	// Add model metadata to logger context for all subsequent logs
	newlog = newlog.With("model_id", modelMetadata.ModelID, "model_url", modelMetadata.URL)

	// Initialize http request
	route := shared.ROUTES[input.Req.Endpoint]
	r, err := http.NewRequest("POST", modelMetadata.URL+route, bytes.NewBuffer(input.Req.Body))
	if err != nil {
		newlog.Warnw("Failed building request", "error", err.Error())
		return nil, &shared.RequestError{
			StatusCode: 400,
			Err:        errors.New("failed building request"),
		}
	}

	// Create headers
	headers := map[string]string{
		"Content-Type": "application/json",
		"Connection":   "keep-alive",
	}

	// Set headers
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	// Handle cold starts - models scaling from 0 can take time to load
	var timeoutOccurred atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), shared.DefaultStreamRequestTimeout)
	timer := time.AfterFunc(shared.DefaultStreamRequestTimeout, func() {
		if input.Req.Stream {
			newlog.Warnw("Stream request timeout triggered",
				"timeout_seconds", shared.DefaultStreamRequestTimeout.Seconds(),
				"model", input.Req.Model,
				"user_id", input.Req.UserID)
			timeoutOccurred.Store(true)
			cancel()
		}
	})
	defer func() {
		timer.Stop()
		cancel()
	}()
	r = r.WithContext(ctx)

	preprocessingTime := time.Since(input.Req.StartTime)
	httpStart := time.Now()

	if input.Ctx.Err() != nil {
		newlog.Warnw("Client already disconnected before HTTP request",
			"context_error", input.Ctx.Err())
	}

	httpClient := im.getHTTPClient(modelMetadata.URL)
	res, err := httpClient.Do(r)
	httpDuration := time.Since(httpStart)
	httpCompletedAt := time.Now()

	defer func() {
		if res != nil && res.Body != nil {
			if closeErr := res.Body.Close(); closeErr != nil {
				newlog.Warnw("Failed to close response body", "error", closeErr)
			}
		}
	}()

	canceled := input.Ctx.Err() == context.Canceled
	modelLabel := fmt.Sprintf("%d-%s", modelMetadata.ModelID, input.Req.Model)

	if err != nil {
		newlog.Errorw("HTTP request failed",
			"http_duration_ms", httpDuration.Milliseconds(),
			"error", err.Error(),
			"canceled", canceled,
			"timeout_occurred", timeoutOccurred.Load())
	}

	if err != nil && timeoutOccurred.Load() {
		newlog.Warnw("Request timed out - likely due to model cold start")
		metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "cold_start").Inc()
		return nil, &shared.RequestError{StatusCode: 503, Err: errors.New("cold start detected, please try again in a few minutes")}
	}

	if canceled {
		newlog.Warnw("Request canceled by client",
			"http_duration_ms", httpDuration.Milliseconds(),
			"elapsed_since_start_ms", time.Since(input.Req.StartTime).Milliseconds(),
			"had_error", err != nil,
			"will_continue_processing", true)
		metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "client_canceled").Inc()
		// Don't return error, let it process gracefully
	}

	if err != nil && !canceled {
		newlog.Warnw("Failed to send request",
			"error", err,
			"http_duration_ms", httpDuration.Milliseconds(),
			"elapsed_since_start_ms", time.Since(input.Req.StartTime).Milliseconds())
		metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "request_failed").Inc()
		return nil, &shared.RequestError{StatusCode: 502, Err: errors.New("request failed")}
	}
	if res != nil && res.StatusCode != http.StatusOK && !canceled {
		newlog.Warnw("Request failed with non-200 status",
			"status_code", res.StatusCode,
			"status", res.Status,
			"http_duration_ms", httpDuration.Milliseconds(),
			"elapsed_since_start_ms", time.Since(input.Req.StartTime).Milliseconds(),
			"returning_early", true)
		metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "request_failed_from_error_code").Inc()
		return nil, &shared.RequestError{StatusCode: res.StatusCode, Err: errors.New("request failed")}
	}

	// Stream back response
	var ttft time.Duration
	var responses []json.RawMessage
	responseContent := ""
	var ttftRecorded bool
	hasDone := false

	if input.Req.Stream && !canceled {
		reader := bufio.NewScanner(res.Body)
		var currentEvent string

		clientDisconnected := false
	scanner:
		for reader.Scan() {
			select {
			case <-ctx.Done():
				newlog.Warnw("Inference engine request timeout during streaming")
				break scanner
			case <-input.Ctx.Done():
				if !clientDisconnected {
					newlog.Warnw("Client disconnected during streaming, continuing to read from inference engine")
					clientDisconnected = true
				}
			default:
				token := reader.Text()

				// Skip empty lines
				if token == "" {
					continue
				}

				// Stream token to client immediately via callback (if provided and client still connected)
				if input.StreamWriter != nil && !clientDisconnected {
					if err := input.StreamWriter(token); err != nil {
						newlog.Warnw("Stream writer returned error, client likely disconnected", "error", err)
						clientDisconnected = true
					}
				}

				// Handle Responses API event format
				if ce, found := strings.CutPrefix(token, "event: "); found {
					currentEvent = ce
					// Check for completion event
					if currentEvent == "response.completed" {
						hasDone = true
					}
					continue
				}

				if !strings.HasPrefix(token, "data: ") {
					continue
				}

				if !ttftRecorded {
					ttft = time.Since(input.Req.StartTime)
					ttftRecorded = true
					timer.Stop()
					// Time from HTTP completion to first token = actual model processing/queue time
					modelProcessingTime := time.Since(httpCompletedAt)
					newlog.Infow("First token received",
						"ttft_ms", ttft.Milliseconds(),
						"preprocessing_ms", preprocessingTime.Milliseconds(),
						"http_duration_ms", httpDuration.Milliseconds(),
						"model_processing_ms", modelProcessingTime.Milliseconds())
				}

				jsonData := strings.TrimPrefix(token, "data: ")

				if jsonData == "[DONE]" {
					hasDone = true
					break scanner
				}

				var rawMessage json.RawMessage
				err := json.Unmarshal([]byte(jsonData), &rawMessage)
				if err != nil {
					newlog.Warnw("failed unmarshaling streamed data", "error", err, "token", token)
					continue
				}
				responses = append(responses, rawMessage)
			}
		}

		responseJSON, err := json.Marshal(responses)
		if err == nil {
			responseContent = string(responseJSON)
		}
		if !hasDone && ctx.Err() == nil {
			newlog.Errorw("encountered streaming error - no [DONE] marker",
				"error", errors.New("[DONE] not found"),
				"responses_received", len(responses),
				"ttft_recorded", ttftRecorded,
				"timeout_occurred", timeoutOccurred.Load())
			metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "streaming_no_done").Inc()
		}
		if !hasDone && ctx.Err() != nil {
			newlog.Warnw("streaming incomplete due to context cancellation",
				"context_error", ctx.Err(),
				"responses_received", len(responses),
				"ttft_recorded", ttftRecorded,
				"timeout_occurred", timeoutOccurred.Load(),
				"total_elapsed_ms", time.Since(input.Req.StartTime).Milliseconds(),
				"time_spent_in_http_ms", httpDuration.Milliseconds(),
				"time_spent_streaming_ms", time.Since(httpCompletedAt).Milliseconds())
		}
		if err := reader.Err(); err != nil && !errors.Is(err, context.Canceled) {
			newlog.Errorw("encountered streaming error", "error", err)
			metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "streaming").Inc()
		}
	}

	if !input.Req.Stream && !canceled { // Handle non-streaming response
		bodyBytes, err := io.ReadAll(res.Body)
		hasDone = true
		if err != nil {
			hasDone = false
		}
		if err != nil && ctx.Err() == nil {
			newlog.Warnw("Failed to read non-streaming response body", "error", err)
			metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "query_model").Inc()
			return nil, &shared.RequestError{StatusCode: 500, Err: errors.New("failed to read response body")}
		}
		responseContent = string(bodyBytes)

		// Calculate timing breakdown
		ttft = time.Since(input.Req.StartTime)
	}

	resInfo := &shared.ResponseInfo{
		Canceled:         input.Ctx.Err() == context.Canceled,
		Completed:        hasDone,
		TotalTime:        time.Since(input.Req.StartTime),
		TimeToFirstToken: ttft,
		ResponseContent:  responseContent,
		ModelID:          modelMetadata.ModelID,
		Cost: shared.ResponseInfoCost{
			InputCredits:    modelMetadata.ICPT,
			OutputCredits:   modelMetadata.OCPT,
			CanceledCredits: modelMetadata.CRC,
		},
	}

	// Log final request state
	newlog.Infow("Request completed",
		"completed", resInfo.Completed,
		"canceled", resInfo.Canceled,
		"ttft_ms", ttft.Milliseconds(),
		"total_ms", resInfo.TotalTime.Milliseconds())

	return resInfo, nil
}
