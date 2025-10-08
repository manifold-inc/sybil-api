// Package shared
package shared

import (
	"fmt"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

func SafeEnv(env string) (string, error) {
	// Lookup env variable, and panic if not present
	res, present := os.LookupEnv(env)
	if !present {
		return "", fmt.Errorf("missing environment variable %s", env)
	}
	return res, nil
}

func GetEnv(env, fallback string) string {
	if value, ok := os.LookupEnv(env); ok {
		return value
	}
	return fallback
}

func ExtractAPIKey(c echo.Context) (string, error) {
	// Check Authorization header
	auth := c.Request().Header.Get("Authorization")
	if auth == "" {
		return "", ErrMissingAuth
	}

	// Validate bearer format
	parts := strings.Split(auth, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", ErrInvalidFormat
	}

	apiKey := parts[1]

	// Validate key length
	if len(apiKey) != APIKeyLength {
		return "", ErrInvalidKeyLen
	}

	return apiKey, nil
}

func GetString(m map[string]any, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func GetFirstMap(arr []any) map[string]any {
	if len(arr) > 0 {
		if m, ok := arr[0].(map[string]any); ok {
			return m
		}
	}
	return nil
}

func DerefString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

// CalculateCredits calculates the number of credits used based on token usage and model
func CalculateCredits(usage *Usage, icpt uint64, ocpt uint64, crc uint64) uint64 {
	if usage == nil {
		return 0
	}
	if usage.IsCanceled {
		return crc
	}
	inputCredits := icpt * usage.PromptTokens
	outputCredits := ocpt * usage.CompletionTokens

	// Calculate total cost using the model's cpt
	return inputCredits + outputCredits
}
