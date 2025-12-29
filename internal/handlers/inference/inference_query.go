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
func (im *InferenceHandler) QueryModels(input QueryInput) (*InferenceOutput, error) {
	// Discover inference service
	modelMetadata, err := im.DiscoverModels(input.Ctx, input.Req.UserID, input.Req.Model)
	if err != nil {
		return nil, errors.Join(&shared.RequestError{
			StatusCode: 404,
			Err:        errors.New("model not found"),
		}, err)
	}

	// Initialize http request
	route := shared.ROUTES[input.Req.Endpoint]
	r, err := http.NewRequest("POST", modelMetadata.URL+route, bytes.NewBuffer(input.Req.Body))
	if err != nil {
		return nil, errors.Join(&shared.RequestError{
			StatusCode: 400,
			Err:        errors.New("failed building request"),
		}, err)
	}

	// Create headers
	headers := map[string]string{
		"Content-Type": "application/json",
		"Connection":   "keep-alive",
		"X-Request-ID": input.Req.ID,
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
			timeoutOccurred.Store(true)
			cancel()
		}
	})
	defer func() {
		timer.Stop()
		cancel()
	}()
	r = r.WithContext(ctx)

	httpClient := im.getHTTPClient(modelMetadata.URL)
	res, err := httpClient.Do(r)

	defer func() {
		if res != nil && res.Body != nil {
			if closeErr := res.Body.Close(); closeErr != nil {
				im.Log.Warnw("Failed to close response body", "error", closeErr)
			}
		}
	}()

	canceled := input.Ctx.Err() == context.Canceled
	modelLabel := fmt.Sprintf("%d-%s", modelMetadata.ModelID, input.Req.Model)

	// Case coldstart
	if err != nil && timeoutOccurred.Load() {
		metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "cold_start").Inc()
		return nil, &shared.RequestError{StatusCode: 503, Err: errors.New("cold start detected, please try again in a few minutes")}
	}

	if err != nil {
		return nil, errors.Join(shared.ErrInternalServerError, errors.New("http req to model failed"), err)
	}

	if res != nil && res.StatusCode != http.StatusOK {
		metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "request_failed_from_error_code").Inc()
		return nil, errors.Join(&shared.RequestError{StatusCode: res.StatusCode, Err: errors.New("downstream request failed")})
	}

	// Stream back response
	var ttft time.Duration
	var responses []json.RawMessage
	responseContent := ""
	var ttftRecorded bool
	hasDone := false
	var errs error

	if input.Req.Stream && !canceled {
		reader := bufio.NewScanner(res.Body)
		var currentEvent string

		clientDisconnected := false
	scanner:
		for reader.Scan() {
			select {
			case <-ctx.Done():
				break scanner
			case <-input.Ctx.Done():
				if !clientDisconnected {
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
				}

				jsonData := strings.TrimPrefix(token, "data: ")

				if jsonData == "[DONE]" {
					hasDone = true
					break scanner
				}

				var rawMessage json.RawMessage
				err := json.Unmarshal([]byte(jsonData), &rawMessage)
				if err != nil {
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
			errs = errors.Join(errs, errors.New("no [DONE] marker"))
			metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "streaming_no_done").Inc()
		}
		if !hasDone && ctx.Err() != nil {
			errs = errors.Join(errs, errors.New("context was unexpectedly canceleld"))
		}
		if err := reader.Err(); err != nil && !errors.Is(err, context.Canceled) {
			errs = errors.Join(errors.New("encountered streaming error"), err)
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
			errs = errors.Join(errors.New("failed to read non-streaming response body"), err)
			metrics.ErrorCount.WithLabelValues(modelLabel, input.Req.Endpoint, fmt.Sprintf("%d", input.Req.UserID), "query_model").Inc()
			return nil, errors.Join(&shared.RequestError{StatusCode: 500, Err: errors.New("failed to read response body")}, errs)
		}
		responseContent = string(bodyBytes)

		// Calculate timing breakdown
		ttft = time.Since(input.Req.StartTime)
	}

	resInfo := &InferenceOutput{
		Metadata: &InferenceMetadata{
			Canceled:         input.Ctx.Err() == context.Canceled,
			Completed:        hasDone,
			TotalTime:        time.Since(input.Req.StartTime),
			TimeToFirstToken: ttft,
			ModelID:          modelMetadata.ModelID,
			Stream:           input.Req.Stream,
			ModelName:        input.Req.Model,
		},
		FinalResponse: []byte(responseContent),
		ModelCost: &shared.ResponseInfoCost{
			InputCredits:    modelMetadata.ICPT,
			OutputCredits:   modelMetadata.OCPT,
			CanceledCredits: modelMetadata.CRC,
		},
		Error: errs,
	}

	return resInfo, nil
}
