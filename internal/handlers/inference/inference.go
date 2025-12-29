// Package inference
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"sybil-api/internal/shared"
)

type InferenceInput struct {
	Req          *shared.RequestInfo
	User         shared.UserMetadata
	Ctx          context.Context
	LogFields    map[string]string
	StreamWriter func(token string) error // callback for real-time streaming
}

type InferenceMetadata struct {
	Stream           bool
	Completed        bool
	Canceled         bool
	ModelID          uint64
	ModelName        string
	TimeToFirstToken time.Duration
	TotalTime        time.Duration
}

type InferenceOutput struct {
	FinalResponse []byte
	ModelCost     *shared.ResponseInfoCost
	Metadata      *InferenceMetadata

	// This is for mid-stream errors, generally.
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

	im.usageCache.AddInFlightToBucket(reqInfo.UserID)
	mu := sync.Mutex{}
	mu.Lock()
	defer func() {
		im.usageCache.RemoveInFlightFromBucket(reqInfo.UserID)
		mu.Unlock()
	}()

	queryLogFields := map[string]string{}
	if input.LogFields != nil {
		maps.Copy(queryLogFields, input.LogFields)
	}
	queryLogFields["model"] = reqInfo.Model
	queryLogFields["stream"] = fmt.Sprintf("%t", reqInfo.Stream)

	queryInput := QueryInput{
		Ctx:          input.Ctx,
		Req:          reqInfo,
		LogFields:    queryLogFields,
		StreamWriter: input.StreamWriter, // Pass the callback through
	}

	resInfo, qerr := im.QueryModels(queryInput)
	if qerr != nil {
		return nil, qerr
	}

	if len(resInfo.FinalResponse) == 0 || !resInfo.Metadata.Completed {
		return resInfo, nil
	}

	go func() {
		var usage *shared.Usage
		switch true {
		case !resInfo.Metadata.Completed:
			break
		case reqInfo.Stream:
			var chunks []map[string]any
			err := json.Unmarshal(resInfo.FinalResponse, &chunks)
			if err != nil {
				im.Log.Warnw(
					"Failed to unmarshal streaming ResponseContent as JSON array of chunks",
					"error",
					err,
					"raw_response_content",
					string(resInfo.FinalResponse),
				)
				break
			}
			for i := len(chunks) - 1; i >= 0; i-- {
				usageData, usageFieldExists := chunks[i]["usage"]
				if usageFieldExists && usageData != nil {
					if extractedUsage, extractErr := extractUsageData(chunks[i], reqInfo.Endpoint); extractErr == nil {
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
		case !reqInfo.Stream:
			var singleResponse map[string]any
			err := json.Unmarshal(resInfo.FinalResponse, &singleResponse)
			if err != nil {
				im.Log.Warnw(
					"Failed to unmarshal non-streaming ResponseContent as single JSON object",
					"error",
					err,
					"raw_response_content",
					string(resInfo.FinalResponse),
				)
				break
			}
			usageData, usageFieldExists := singleResponse["usage"]
			if usageFieldExists && usageData != nil {
				if extractedUsage, extractErr := extractUsageData(singleResponse, reqInfo.Endpoint); extractErr == nil {
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
			usage = &shared.Usage{IsCanceled: resInfo.Metadata.Canceled}
		}

		totalCredits := shared.CalculateCredits(usage, resInfo.ModelCost.InputCredits, resInfo.ModelCost.OutputCredits, resInfo.ModelCost.CanceledCredits)

		pqi := &shared.ProcessedQueryInfo{
			UserID:           reqInfo.UserID,
			Model:            reqInfo.Model,
			ModelID:          resInfo.Metadata.ModelID,
			Endpoint:         reqInfo.Endpoint,
			TotalTime:        resInfo.Metadata.TotalTime,
			TimeToFirstToken: resInfo.Metadata.TimeToFirstToken,
			Usage:            usage,
			Cost:             *resInfo.ModelCost,
			TotalCredits:     totalCredits,
			ResponseContent:  string(resInfo.FinalResponse),
			RequestContent:   reqInfo.Body,
			CreatedAt:        time.Now(),
			ID:               reqInfo.ID,
		}

		mu.Lock()
		im.usageCache.AddRequestToBucket(reqInfo.UserID, pqi, reqInfo.ID)
		mu.Unlock()
	}()

	return resInfo, nil
}
