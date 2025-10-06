// Package metrics defines prometheus metrics to expose
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "tha:request_duration_seconds",
			Help:    "Total time taken for requests in seconds",
			Buckets: []float64{1, 2.5, 5, 10, 15, 20, 25, 30, 40, 50, 75, 100, 150, 200, 350, 400, 500, 600},
		},
		[]string{"model", "endpoint"},
	)

	TimeToFirstToken = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "tha:time_to_first_token_seconds",
			Help:    "Time to first token in seconds",
			Buckets: []float64{.5, 1, 2.5, 5, 10, 15, 20, 25, 30, 40, 50, 75, 100, 150, 200, 350, 400, 500, 600},
		},
		[]string{"model", "endpoint"},
	)

	PromptTokens = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tha:prompt_tokens_total",
			Help: "Total number of prompt tokens used",
		},
		[]string{"model", "endpoint"},
	)

	CompletionTokens = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tha:completion_tokens_total",
			Help: "Total number of completion tokens used",
		},
		[]string{"model", "endpoint"},
	)

	TotalTokens = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tha:total_tokens_total",
			Help: "Total number of tokens used",
		},
		[]string{"model", "endpoint"},
	)

	RequestCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tha:request_count_total",
			Help: "Total number of requests processed",
		},
		[]string{"model", "endpoint", "status"},
	)

	CreditUsage = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tha:credit_usage_total",
			Help: "Total credits used",
		},
		[]string{"model", "endpoint", "credit_type"},
	)

	TokensPerSecond = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "tha:tokens_per_second",
			Help:    "Tokens per second",
			Buckets: []float64{1, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 60, 70, 80},
		},
		[]string{"model", "endpoint"},
	)

	InflightRequests = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "tha:inflight_requests",
			Help: "Current Inflight Requests",
		},
		[]string{"user_id"},
	)

	CanceledRequests = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "tha:canceled_requests",
			Help: "Canceled Requests",
		},
		[]string{"user_id"},
	)

	OverloadCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tha:overload_count",
			Help: "Requests rejected from overload",
		},
		[]string{"model", "endpoint"},
	)

	ErrorCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tha:error_count",
			Help: "Error count",
		},
		[]string{"model", "endpoint", "user_id", "from"},
	)
	ResponseCodes = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tha:status_code",
			Help: "Status Codes",
		},
		[]string{"path", "status_code", "model"},
	)
)
