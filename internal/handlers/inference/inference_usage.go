package inference

import (
	"errors"
	"fmt"

	"sybil-api/internal/shared"
)

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

