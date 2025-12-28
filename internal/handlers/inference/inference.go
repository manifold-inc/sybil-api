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
	Usage            *shared.Usage
	Completed        bool
	Canceled         bool
	ModelID          string
	TimeToFirstToken time.Duration
	TotalTime        time.Duration
}

type InferenceOutput struct {
	FinalResponse []byte
	Metadata      *InferenceMetadata
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

	meta := &InferenceMetadata{
		Stream:           reqInfo.Stream,
		ModelID:          fmt.Sprintf("%d", resInfo.ModelID),
		TimeToFirstToken: resInfo.TimeToFirstToken,
		TotalTime:        resInfo.TotalTime,
		Usage:            resInfo.Usage,
		Completed:        resInfo.Completed,
		Canceled:         resInfo.Canceled,
	}
	output := &InferenceOutput{Metadata: meta, FinalResponse: []byte(resInfo.ResponseContent)}

	if resInfo.ResponseContent == "" || !resInfo.Completed {
		return output, nil
	}

	go func() {
		switch true {
		case !resInfo.Completed:
			break
		case reqInfo.Stream:
			var chunks []map[string]any
			err := json.Unmarshal([]byte(resInfo.ResponseContent), &chunks)
			if err != nil {
				im.Log.Warnw(
					"Failed to unmarshal streaming ResponseContent as JSON array of chunks",
					"error",
					err,
					"raw_response_content",
					resInfo.ResponseContent,
				)
				break
			}
			for i := len(chunks) - 1; i >= 0; i-- {
				usageData, usageFieldExists := chunks[i]["usage"]
				if usageFieldExists && usageData != nil {
					if extractedUsage, extractErr := extractUsageData(chunks[i], reqInfo.Endpoint); extractErr == nil {
						resInfo.Usage = extractedUsage
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
			err := json.Unmarshal([]byte(resInfo.ResponseContent), &singleResponse)
			if err != nil {
				im.Log.Warnw(
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
				if extractedUsage, extractErr := extractUsageData(singleResponse, reqInfo.Endpoint); extractErr == nil {
					resInfo.Usage = extractedUsage
					break
				}
				im.Log.Warnw(
					"Failed to extract usage data from single response object that had a non-null usage field",
				)
			}
		default:
			break
		}

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

		mu.Lock()
		im.usageCache.AddRequestToBucket(reqInfo.UserID, pqi, reqInfo.ID)
		mu.Unlock()
	}()

	return output, nil
}
