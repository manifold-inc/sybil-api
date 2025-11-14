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
	"sybil-api/internal/metrics"
	"sybil-api/internal/shared"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
)

func (im *InferenceManager) QueryModels(
	inv *Invocation,
	req *shared.RequestInfo,
	resp Responder,
) (*shared.ResponseInfo, *shared.RequestError) {
	// Use context from invocation
	ctx := inv.Ctx

	// Discover inference service
	modelMetadata, err := im.DiscoverModels(ctx, req.UserID, req.Model)
	if err != nil {
		inv.Log.Errorw("Service discovery failed", "error", err)
		return nil, &shared.RequestError{
			StatusCode: 404,
			Err:        fmt.Errorf("service not found: %w", err),
		}
	}

	// Add model metadata to logger context for all subsequent logs
	inv.Log = inv.Log.With("model_id", modelMetadata.ModelID, "model_url", modelMetadata.URL)

	// Initialize http request
	route := shared.ROUTES[req.Endpoint]
	r, err := http.NewRequest("POST", modelMetadata.URL+route, bytes.NewBuffer(req.Body))
	if err != nil {
		inv.Log.Warnw("Failed building request", "error", err.Error())
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
		if req.Stream {
			inv.Log.Warnw("Stream request timeout triggered",
				"timeout_seconds", shared.DefaultStreamRequestTimeout.Seconds(),
				"model", req.Model,
				"user_id", req.UserID)
			timeoutOccurred.Store(true)
			cancel()
		}
	})
	defer func() {
		timer.Stop()
		cancel()
	}()
	r = r.WithContext(ctx)

	preprocessingTime := time.Since(req.StartTime)
	httpStart := time.Now()

	if ctx.Err() != nil {
		inv.Log.Warnw("Client already disconnected before HTTP request",
			"context_error", ctx.Err())
	}

	httpClient := im.getHTTPClient(modelMetadata.URL)
	res, err := httpClient.Do(r)
	httpDuration := time.Since(httpStart)
	httpCompletedAt := time.Now()

	defer func() {
		if res != nil && res.Body != nil {
			if closeErr := res.Body.Close(); closeErr != nil {
				inv.Log.Warnw("Failed to close response body", "error", closeErr)
			}
		}
	}()

	canceled := ctx.Err() == context.Canceled
	modelLabel := fmt.Sprintf("%d-%s", modelMetadata.ModelID, req.Model)

	if err != nil {
		inv.Log.Errorw("HTTP request failed",
			"http_duration_ms", httpDuration.Milliseconds(),
			"error", err.Error(),
			"canceled", canceled,
			"timeout_occurred", timeoutOccurred.Load())
	}

	if err != nil && timeoutOccurred.Load() {
		inv.Log.Warnw("Request timed out - likely due to model cold start")
		metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "cold_start").Inc()
		return nil, &shared.RequestError{StatusCode: 503, Err: errors.New("cold start detected, please try again in a few minutes")}
	}

	if canceled {
		inv.Log.Warnw("Request canceled by client",
			"http_duration_ms", httpDuration.Milliseconds(),
			"elapsed_since_start_ms", time.Since(req.StartTime).Milliseconds(),
			"had_error", err != nil,
			"will_continue_processing", true)
		metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "client_canceled").Inc()
		// Don't return error, let it process gracefully
	}

	if err != nil && !canceled {
		inv.Log.Warnw("Failed to send request",
			"error", err,
			"http_duration_ms", httpDuration.Milliseconds(),
			"elapsed_since_start_ms", time.Since(req.StartTime).Milliseconds())
		metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "request_failed").Inc()
		return nil, &shared.RequestError{StatusCode: 502, Err: errors.New("request failed")}
	}
	if res != nil && res.StatusCode != http.StatusOK && !canceled {
		inv.Log.Warnw("Request failed with non-200 status",
			"status_code", res.StatusCode,
			"status", res.Status,
			"http_duration_ms", httpDuration.Milliseconds(),
			"elapsed_since_start_ms", time.Since(req.StartTime).Milliseconds(),
			"returning_early", true)
		metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "request_failed_from_error_code").Inc()
		return nil, &shared.RequestError{StatusCode: res.StatusCode, Err: errors.New("request failed")}
	}

	// Stream back response
	var ttft time.Duration
	var responses []json.RawMessage
	responseContent := ""
	var ttftRecorded bool
	hasDone := false

	if req.Stream && !canceled { // Check if the request is streaming
		resp.SetHeader("Content-Type", "text/event-stream")
		reader := bufio.NewScanner(res.Body)
		var currentEvent string

		clientDisconnected := false
	scanner:
		for reader.Scan() {
			select {
			case <-ctx.Done():
				inv.Log.Warnw("Inference engine request timeout during streaming")
				break scanner
			case <-ctx.Done():
				if !clientDisconnected {
					inv.Log.Warnw("Client disconnected during streaming, continuing to read from inference engine")
					clientDisconnected = true
				}
			default:
				token := reader.Text()

				// Skip empty lines
				if token == "" {
					continue
				}

			// Only write to client if they're still connected
			if ctx.Err() == nil {
				_ = resp.SendChunk([]byte(token + "\n\n"))
				_ = resp.Flush()
			}

				// Handle Responses API event format
				if strings.HasPrefix(token, "event: ") {
					currentEvent = strings.TrimPrefix(token, "event: ")
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
					ttft = time.Since(req.StartTime)
					ttftRecorded = true
					timer.Stop()
					// Time from HTTP completion to first token = actual model processing/queue time
					modelProcessingTime := time.Since(httpCompletedAt)
					inv.Log.Infow("First token received",
						"ttft_ms", ttft.Milliseconds(),
						"preprocessing_ms", preprocessingTime.Milliseconds(),
						"http_duration_ms", httpDuration.Milliseconds(),
						"model_processing_ms", modelProcessingTime.Milliseconds())
				}

				// Handle Chat/Completions [DONE]
				if token == "data: [DONE]" {
					hasDone = true
					break scanner
				}

				// Extract the JSON part
				jsonData := strings.TrimPrefix(token, "data: ")
				var rawMessage json.RawMessage
				err := json.Unmarshal([]byte(jsonData), &rawMessage)
				if err != nil {
					inv.Log.Warnw("failed unmarshaling streamed data", "error", err, "token", token)
					continue
				}
				responses = append(responses, rawMessage)
			}
		}

		// Always collect response content since saving decision is made in ProcessOpenaiRequest
		responseJSON, err := json.Marshal(responses)
		if err == nil {
			responseContent = string(responseJSON)
		}
		if !hasDone && ctx.Err() == nil {
			inv.Log.Errorw("encountered streaming error - no [DONE] marker",
				"error", errors.New("[DONE] not found"),
				"responses_received", len(responses),
				"ttft_recorded", ttftRecorded,
				"timeout_occurred", timeoutOccurred.Load())
			metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "streaming_no_done").Inc()
		}
		if !hasDone && ctx.Err() != nil {
			inv.Log.Warnw("streaming incomplete due to context cancellation",
				"context_error", ctx.Err(),
				"responses_received", len(responses),
				"ttft_recorded", ttftRecorded,
				"timeout_occurred", timeoutOccurred.Load(),
				"total_elapsed_ms", time.Since(req.StartTime).Milliseconds(),
				"time_spent_in_http_ms", httpDuration.Milliseconds(),
				"time_spent_streaming_ms", time.Since(httpCompletedAt).Milliseconds())
		}
		if err := reader.Err(); err != nil && !errors.Is(err, context.Canceled) {
			inv.Log.Errorw("encountered streaming error", "error", err)
			metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "streaming").Inc()
		}
	}

	if !req.Stream && !canceled { // Handle non-streaming response
		bodyBytes, err := io.ReadAll(res.Body)
		hasDone = true
		if err != nil {
			hasDone = false
		}
		if err != nil && ctx.Err() == nil {
			inv.Log.Warnw("Failed to read non-streaming response body", "error", err)
			metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "query_model").Inc()
			return nil, &shared.RequestError{StatusCode: 500, Err: errors.New("failed to read response body")}
		}
		responseContent = string(bodyBytes)
		// For non-streaming, write the entire response body at once and set Content-Type.
		resp.SetHeader(echo.HeaderContentType, echo.MIMEApplicationJSON)
		if ctx.Err() == nil {
			if err := resp.SendChunk(bodyBytes); err != nil {
				inv.Log.Errorw("Failed to write non-streaming response to client", "error", err)
			}
		}

		// Calculate timing breakdown
		ttft = time.Since(req.StartTime)
	}

	resInfo := &shared.ResponseInfo{
		Canceled:         ctx.Err() == context.Canceled,
		Completed:        hasDone,
		TotalTime:        time.Since(req.StartTime),
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
	inv.Log.Infow("Request completed",
		"completed", resInfo.Completed,
		"canceled", resInfo.Canceled,
		"ttft_ms", ttft.Milliseconds(),
		"total_ms", resInfo.TotalTime.Milliseconds())

	return resInfo, nil
}