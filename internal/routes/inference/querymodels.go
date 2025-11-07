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
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
)

// QueryModels forwards the request to the appropriate model
func (im *InferenceManager) QueryModels(c *setup.Context, req *shared.RequestInfo) (*shared.ResponseInfo, *shared.RequestError) {
	// Discover inference service
	modelMetadata, err := im.DiscoverModels(c.Request().Context(), req.UserID, req.Model)
	if err != nil {
		c.Log.Errorw("Service discovery failed", "error", err, "model", req.Model)
		return nil, &shared.RequestError{
			StatusCode: 404,
			Err:        fmt.Errorf("service not found: %w", err),
		}
	}

	// Initialize http request
	route := shared.ROUTES[req.Endpoint]
	r, err := http.NewRequest("POST", modelMetadata.URL+route, bytes.NewBuffer(req.Body))
	if err != nil {
		c.Log.Warnw("Failed building request", "error", err.Error())
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
	ctx, cancel := context.WithCancel(c.Request().Context())
	timer := time.AfterFunc(shared.DefaultStreamRequestTimeout, func() {
		if req.Stream {
			c.Log.Warnw("Stream request timeout triggered",
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

	// Start Request
	requestStart := time.Now()

	// Check if client already disconnected before we start
	if c.Request().Context().Err() != nil {
		c.Log.Infow("Client already disconnected before HTTP request",
			"context_error", c.Request().Context().Err())
	}

	res, err := im.HTTPClient.Do(r)
	requestDuration := time.Since(requestStart)

	defer func() {
		if res != nil && res.Body != nil {
			if closeErr := res.Body.Close(); closeErr != nil {
				c.Log.Warnw("Failed to close response body", "error", closeErr)
			}
		}
	}()

	canceled := c.Request().Context().Err() == context.Canceled
	modelLabel := fmt.Sprintf("%d-%s", modelMetadata.ModelID, req.Model)

	// Log HTTP request completion with status
	if err != nil {
		c.Log.Errorw("HTTP request failed",
			"duration_ms", requestDuration.Milliseconds(),
			"model_url", modelMetadata.URL,
			"model", req.Model,
			"error", err.Error(),
			"canceled", canceled,
			"timeout_occurred", timeoutOccurred.Load())
	} else if res != nil {
		c.Log.Infow("HTTP request completed",
			"duration_ms", requestDuration.Milliseconds(),
			"model_url", modelMetadata.URL,
			"model", req.Model,
			"status_code", res.StatusCode,
			"canceled", canceled)
	}

	// Handle timeout
	if err != nil && timeoutOccurred.Load() {
		c.Log.Warnw("Request timed out - likely due to model cold start", "model", req.Model, "user_id", req.UserID)
		metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "cold_start").Inc()
		return nil, &shared.RequestError{StatusCode: 503, Err: errors.New("cold start detected, please try again in a few minutes")}
	}

	// Handle client cancellation
	if canceled {
		c.Log.Infow("Request canceled by client",
			"model", req.Model,
			"user_id", req.UserID,
			"duration_ms", requestDuration.Milliseconds(),
			"had_error", err != nil)
		metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "client_canceled").Inc()
		// Don't return error, let it process gracefully
	}

	if err != nil && !canceled {
		c.Log.Warnw("Failed to send request", "error", err)
		metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "request_failed").Inc()
		return nil, &shared.RequestError{StatusCode: 502, Err: errors.New("request failed")}
	}
	if res != nil && res.StatusCode != http.StatusOK && !canceled {
		c.Log.Warnw("Request failed", "status_code", res.Status)
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
		c.Response().Header().Set("Content-Type", "text/event-stream")
		reader := bufio.NewScanner(res.Body)
		c.Log.Infow("Starting streaming loop", "model", req.Model)
	scanner:
		for reader.Scan() {
			select {
			case <-ctx.Done():
				c.Log.Warnw("Request canceled by client during streaming")
				break scanner
			default:
				token := reader.Text()

				// Skip empty lines
				if token == "" {
					continue
				}

				_, _ = fmt.Fprint(c.Response(), token+"\n\n")
				c.Response().Flush()

				if !strings.HasPrefix(token, "data: ") {
					c.Log.Warnw("non data response", "text", token)
					continue
				}

				if !ttftRecorded {
					ttft = time.Since(req.StartTime)
					ttftRecorded = true
					timer.Stop()
					queueTime := ttft.Milliseconds() - requestDuration.Milliseconds()
					c.Log.Infow("First token received",
						"ttft_ms", ttft.Milliseconds(),
						"http_request_ms", requestDuration.Milliseconds(),
						"queue_time_ms", queueTime,
						"model", req.Model)
				}
				if token == "data: [DONE]" {
					hasDone = true
					break scanner
				}
				// Extract the JSON part
				jsonData := strings.TrimPrefix(token, "data: ")
				var rawMessage json.RawMessage
				err := json.Unmarshal([]byte(jsonData), &rawMessage)
				if err != nil {
					c.Log.Warnw("failed unmarshaling streamed data", "error", err, "token", token)
					continue
				}
				responses = append(responses, rawMessage)
			}
		}

		// Log why the scanner loop exited
		c.Log.Infow("Streaming loop exited",
			"responses_received", len(responses),
			"ttft_recorded", ttftRecorded,
			"has_done", hasDone,
			"ctx_error", ctx.Err(),
			"scanner_error", reader.Err())

		// Always collect response content since saving decision is made in ProcessOpenaiRequest
		responseJSON, err := json.Marshal(responses)
		if err == nil {
			responseContent = string(responseJSON)
		}
		if !hasDone && ctx.Err() == nil {
			c.Log.Errorw("encountered streaming error",
				"error", errors.New("[DONE] not found"),
				"responses_received", len(responses),
				"ttft_recorded", ttftRecorded)
			metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "streaming_no_done").Inc()
		}
		if !hasDone && ctx.Err() != nil {
			c.Log.Warnw("streaming incomplete due to context cancellation",
				"context_error", ctx.Err(),
				"responses_received", len(responses),
				"ttft_recorded", ttftRecorded)
		}
		if err := reader.Err(); err != nil && !errors.Is(err, context.Canceled) {
			c.Log.Errorw("encountered streaming error", "error", err)
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
			c.Log.Warnw("Failed to read non-streaming response body", "error", err)
			metrics.ErrorCount.WithLabelValues(modelLabel, req.Endpoint, fmt.Sprintf("%d", req.UserID), "query_model").Inc()
			return nil, &shared.RequestError{StatusCode: 500, Err: errors.New("failed to read response body")}
		}
		responseContent = string(bodyBytes)
		// For non-streaming, write the entire response body at once and set Content-Type.
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		if ctx.Err() == nil {
			if _, err := c.Response().Write(bodyBytes); err != nil {
				c.Log.Errorw("Failed to write non-streaming response to client", "error", err)
			}
		}

		// kept just for simplicity
		ttft = time.Since(req.StartTime)
		queueTime := ttft.Milliseconds() - requestDuration.Milliseconds()
		c.Log.Infow("Non-streaming response completed",
			"ttft_ms", ttft.Milliseconds(),
			"http_request_ms", requestDuration.Milliseconds(),
			"queue_time_ms", queueTime,
			"model", req.Model)
	}

	resInfo := &shared.ResponseInfo{
		Canceled:         c.Request().Context().Err() == context.Canceled,
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
	c.Log.Infow("QueryModels completed",
		"canceled", resInfo.Canceled,
		"completed", resInfo.Completed,
		"stream", req.Stream,
		"ttft_ms", ttft.Milliseconds(),
		"total_time_ms", resInfo.TotalTime.Milliseconds(),
		"response_length", len(responseContent))

	return resInfo, nil
}
