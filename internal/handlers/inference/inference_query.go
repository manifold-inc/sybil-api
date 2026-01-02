package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"sybil-api/internal/shared"
)

type QueryInput struct {
	Ctx          context.Context
	Req          *RequestInfo
	StreamWriter func(token string) error // Optional callback for real-time streaming
}

// QueryModels forwards the request to the appropriate model
func (im *InferenceHandler) QueryModels(ctx context.Context, req *RequestInfo, streamWriter func(token string) error) (*InferenceOutput, error) {
	// Initialize http request
	route := shared.ROUTES[req.Endpoint]
	r, err := http.NewRequest("POST", req.ModelMetadata.URL+route, bytes.NewBuffer(req.Body))
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
		"X-Request-ID": req.ID,
	}

	// Set headers
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	// Handle cold starts - models scaling from 0 can take time to load
	var timeoutOccurred atomic.Bool
	rctx, cancel := context.WithTimeout(context.Background(), shared.DefaultStreamRequestTimeout)
	timer := time.AfterFunc(shared.DefaultStreamRequestTimeout, func() {
		// Timer is redundant for non streaming requests
		if req.Stream {
			timeoutOccurred.Store(true)
			cancel()
		}
	})
	defer func() {
		timer.Stop()
		cancel()
	}()
	r = r.WithContext(rctx)

	httpClient := im.getHTTPClient(req.ModelMetadata.URL)
	res, err := httpClient.Do(r)

	defer func() {
		if res != nil && res.Body != nil {
			if closeErr := res.Body.Close(); closeErr != nil {
				im.Log.Warnw("Failed to close response body", "error", closeErr)
			}
		}
	}()

	// Case coldstart
	if err != nil && timeoutOccurred.Load() {
		return nil, errors.Join(&shared.RequestError{StatusCode: 503, Err: errors.New("cold start detected, please try again in a few minutes")}, shared.ErrColdStart)
	}

	if err != nil {
		return nil, errors.Join(shared.ErrInternalServerError, shared.ErrFailedModelReq, err)
	}

	if res != nil && res.StatusCode != http.StatusOK {
		return nil, errors.Join(&shared.RequestError{StatusCode: res.StatusCode, Err: errors.New("downstream request failed")}, shared.ErrFailedModelReqFromCode)
	}

	var errs error

	if !req.Stream { // Handle non-streaming response
		bodyBytes, err := io.ReadAll(res.Body)
		completed := true
		if err != nil {
			completed = false
		}
		if err != nil && rctx.Err() == nil {
			return nil, errors.Join(&shared.RequestError{StatusCode: 500, Err: errors.New("failed to read response body")}, shared.ErrFailedReadingResponse, err)
		}
		resInfo := &InferenceOutput{
			Metadata: &InferenceMetadata{
				Canceled:  ctx.Err() == context.Canceled,
				Completed: completed,
				TotalTime: time.Since(req.StartTime),
			},
			FinalResponse: bodyBytes,
			Error:         errs,
		}
		return resInfo, nil
	}

	// Stream back response
	var ttft time.Duration
	var responses []json.RawMessage
	var ttftRecorded bool
	hasDone := false

	reader := bufio.NewScanner(res.Body)
	var currentEvent string

	clientDisconnected := false
scanner:
	for reader.Scan() {
		select {
		case <-rctx.Done():
			break scanner
		case <-ctx.Done():
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
			if streamWriter != nil && !clientDisconnected {
				if err := streamWriter(token); err != nil {
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
				ttft = time.Since(req.StartTime)
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

	// shouldnt be able to error since responses is already well formatted json
	responseBytes, _ := json.Marshal(responses)
	if rctx.Err() != nil {
		errs = errors.Join(errs, shared.ErrModelContext, rctx.Err())
	}
	if !hasDone {
		errs = errors.Join(errs, shared.ErrMissingDoneToken)
	}

	if reader.Err() != nil && !errors.Is(err, context.Canceled) {
		errs = errors.Join(shared.ErrFailedReadingResponse, err)
	}

	if len(responses) == 0 {
		return nil, errors.Join(&shared.RequestError{Err: errors.New("no response from model"), StatusCode: 500}, errs)
	}

	resInfo := &InferenceOutput{
		Metadata: &InferenceMetadata{
			Canceled:         ctx.Err() == context.Canceled,
			Completed:        hasDone,
			TotalTime:        time.Since(req.StartTime),
			TimeToFirstToken: ttft,
		},
		FinalResponse: responseBytes,
		Error:         errs,
	}

	return resInfo, nil
}
