package shared

import "time"

// HTTP Client Configuration
const (
	DefaultHTTPTimeout         = 180 * time.Second
	DefaultMaxIdleConns        = 100
	DefaultMaxIdleConnsPerHost = 10
	DefaultIdleConnTimeout     = 90 * time.Second
	DefaultMaxConnsPerHost     = 50
	DefaultRequestTimeout      = 120 * time.Second
	DefaultShutdownTimeout     = 10 * time.Minute
)

// Cache Configuration
const (
	ModelServiceCacheTTL = 30 * time.Minute
	UserInfoCacheTTL     = 1 * time.Minute
)

// API Configuration
const (
	DefaultMaxTokens    = 512
	DefaultStreamOption = true
	APIKeyLength        = 32
)

// Polling Configuration
const (
	TargonPollingInterval = 30 * time.Second
	TargonPollingMaxWait  = 60 * time.Minute
	TargonCleanupTimeout  = 30 * time.Second
	PollingMaxAttempts    = 120 // 120 * 30s = 60 minutes
)

// Bucket Configuration
const (
	BucketFlushInterval = 1 * time.Minute
	BucketRetryDelay    = 30 * time.Second
	MaxFlushRetries     = 3
)
