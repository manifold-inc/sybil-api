// Package inference
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"sybil-api/internal/metrics"

	"sybil-api/internal/shared"
)

type InferenceInput struct {
	Req          *RequestInfo
	User         shared.UserMetadata
	Ctx          context.Context
	LogFields    map[string]string
	StreamWriter func(token string) error // callback for real-time streaming
}

type InferenceMetadata struct {
	Completed        bool
	Canceled         bool
	TotalTime        time.Duration
	TimeToFirstToken time.Duration
}

type InferenceOutput struct {
	FinalResponse []byte
	Metadata      *InferenceMetadata

	// This is for mid-stream errors, if any
	Error error
}

// DoInference only returns errors from bad inputs and when output would not exist.
// A partial stream for instance would not return an error directly but be baked inside of
// InferenceOutput. The difference is that if we get an error back from DoInference,
// we can assume no http status code was sent and the router should send them accordingly
func (im *InferenceHandler) DoInference(input InferenceInput) (*InferenceOutput, error) {
	if input.Req == nil {
		return nil, &shared.RequestError{
			StatusCode: 400,
			Err:        errors.New("request info missing"),
		}
	}
	reqInfo := input.Req

	// Make sure to remove in flights if they arent going to be picked up by
	// AddRequestToBucket
	im.usageCache.AddInFlightToBucket(reqInfo.UserID)

	resInfo, qerr := im.QueryModels(input.Ctx, reqInfo, input.StreamWriter)
	if qerr != nil {
		im.usageCache.RemoveInFlightFromBucket(reqInfo.UserID)
		return nil, qerr
	}

	go im.PostProcess(reqInfo, resInfo)
	return resInfo, nil
}

// PostProcess deducts user credits and saves metadata to the db / metrics ( for now )
func (im *InferenceHandler) PostProcess(req *RequestInfo, res *InferenceOutput) {
	var usage *shared.Usage
	switch req.Stream {
	case true:
		var chunks []map[string]any
		err := json.Unmarshal(res.FinalResponse, &chunks)
		if err != nil {
			im.Log.Warnw(
				"Failed to unmarshal streaming ResponseContent as JSON array of chunks",
				"error",
				err,
				"raw_response_content",
				string(res.FinalResponse),
			)
			break
		}
		for i := len(chunks) - 1; i >= 0; i-- {
			usageData, usageFieldExists := chunks[i]["usage"]
			if usageFieldExists && usageData != nil {
				if extractedUsage, extractErr := extractUsageData(chunks[i], req.Endpoint); extractErr == nil {
					usage = extractedUsage
					break
				}
				im.Log.Warnw(
					"Failed to extract usage data from a response chunk that had a non-null usage field",
					"chunk_index",
					i,
				)
				break
			}
		}
	case false:
		var singleResponse map[string]any
		err := json.Unmarshal(res.FinalResponse, &singleResponse)
		if err != nil {
			im.Log.Warnw(
				"Failed to unmarshal non-streaming ResponseContent as single JSON object",
				"error",
				err,
				"raw_response_content",
				string(res.FinalResponse),
			)
			break
		}
		usageData, usageFieldExists := singleResponse["usage"]
		if usageFieldExists && usageData != nil {
			if extractedUsage, extractErr := extractUsageData(singleResponse, req.Endpoint); extractErr == nil {
				usage = extractedUsage
				break
			}
			im.Log.Warnw(
				"Failed to extract usage data from single response object that had a non-null usage field",
			)
		}
	default:
		break
	}

	if usage == nil {
		usage = &shared.Usage{IsCanceled: res.Metadata.Canceled}
	}

	totalCredits := shared.CalculateCredits(usage, req.ModelMetadata.ICPT, req.ModelMetadata.OCPT, req.ModelMetadata.CRC)

	pqi := &shared.ProcessedQueryInfo{
		UserID:           req.UserID,
		Model:            req.Model,
		ModelID:          req.ModelMetadata.ModelID,
		Endpoint:         req.Endpoint,
		TimeToFirstToken: res.Metadata.TimeToFirstToken,
		TotalTime:        res.Metadata.TotalTime,
		Usage:            usage,
		TotalCredits:     totalCredits,
		CreatedAt:        time.Now(),
	}

	im.usageCache.AddRequestToBucket(req.UserID, pqi, req.ID)

	modelLabel := fmt.Sprintf("%d-%s", req.ModelMetadata.ModelID, req.Model)

	if res.Metadata.TimeToFirstToken != time.Duration(0) {
		metrics.TimeToFirstToken.WithLabelValues(modelLabel, req.Endpoint).Observe(res.Metadata.TimeToFirstToken.Seconds())
	}
	metrics.CreditUsage.WithLabelValues(modelLabel, req.Endpoint, "total").Add(float64(totalCredits))
	metrics.RequestCount.WithLabelValues(modelLabel, req.Endpoint, "success").Inc()
	if usage != nil {
		metrics.TokensPerSecond.WithLabelValues(modelLabel, req.Endpoint).Observe(float64(usage.CompletionTokens) / res.Metadata.TotalTime.Seconds())
		metrics.PromptTokens.WithLabelValues(modelLabel, req.Endpoint).Add(float64(usage.PromptTokens))
		metrics.CompletionTokens.WithLabelValues(modelLabel, req.Endpoint).Add(float64(usage.CompletionTokens))
		metrics.TotalTokens.WithLabelValues(modelLabel, req.Endpoint).Add(float64(usage.TotalTokens))
		if usage.IsCanceled {
			metrics.CanceledRequests.WithLabelValues(modelLabel, fmt.Sprintf("%d", req.UserID)).Inc()
		}
	}
}
