package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type InferenceOutput struct {
	Stream           bool
	FinalResponse    []byte
	ModelID          string
	TimeToFirstToken time.Duration
	TotalTime        time.Duration
	Usage            *shared.Usage
	Completed        bool
	Canceled         bool
}

func (im *InferenceManager) DoInference(input InferenceInput) (*InferenceOutput, *shared.RequestError) {
	if input.Req == nil {
		return nil, &shared.RequestError{
			StatusCode: 500,
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
		for k, v := range input.LogFields {
			queryLogFields[k] = v
		}
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

	output := &InferenceOutput{
		Stream:           reqInfo.Stream,
		ModelID:          fmt.Sprintf("%d", resInfo.ModelID),
		TimeToFirstToken: resInfo.TimeToFirstToken,
		TotalTime:        resInfo.TotalTime,
		Usage:            resInfo.Usage,
		Completed:        resInfo.Completed,
		Canceled:         resInfo.Canceled,
		FinalResponse:    []byte(resInfo.ResponseContent),
	}

	log := im.Log.With(
		"endpoint", reqInfo.Endpoint,
		"user_id", input.User.UserID,
		"request_id", reqInfo.ID,
	)

	if resInfo.ResponseContent == "" || !resInfo.Completed {
		log.Errorw("No response or incomplete response from model",
			"response_content_length", len(resInfo.ResponseContent),
			"completed", resInfo.Completed,
			"canceled", resInfo.Canceled,
			"ttft", resInfo.TimeToFirstToken,
			"total_time", resInfo.TotalTime)
		return nil, &shared.RequestError{
			StatusCode: 500,
			Err:        errors.New("no response from model"),
		}
	}

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
			for i := len(chunks) - 1; i >= 0; i-- {
				usageData, usageFieldExists := chunks[i]["usage"]
				if usageFieldExists && usageData != nil {
					if extractedUsage, extractErr := extractUsageData(chunks[i], reqInfo.Endpoint); extractErr == nil {
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
				if extractedUsage, extractErr := extractUsageData(singleResponse, reqInfo.Endpoint); extractErr == nil {
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
