package shared

import (
	"fmt"
	"time"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type Response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Delta   *Delta   `json:"delta,omitempty"`
	Message *Message `json:"message,omitempty"`
}
type Delta struct {
	Content string `json:"content"`
}
type Message struct {
	Content string `json:"content"`
	Role    string `json:"role,omitempty"`
}

type InferenceBody struct {
	Messages    []ChatMessage `json:"messages"`
	Temperature float32       `json:"temperature"`
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	Stream      bool          `json:"stream"`
	Logprobs    bool          `json:"logprobs"`
	HistoryID   *string       `json:"history_id,omitempty"`
}

type SearchResults struct {
	URL           *string   `json:"url,omitempty"`
	Source        *string   `json:"source,omitempty"`
	Resolution    *string   `json:"resolution,omitempty"`
	ImgSource     *string   `json:"img_src,omitempty"`
	Title         *string   `json:"title,omitempty"`
	Content       *string   `json:"content,omitempty"`
	Thumbnail     *string   `json:"thumbnail,omitempty"`
	ParsedURL     *[]string `json:"parsed_url,omitempty"`
	Metadata      *string   `json:"metadata,omitempty"`
	PublishedDate *string   `json:"publishedDate,omitempty"`
}

type SearchResponseBody struct {
	Query           string          `json:"query"`
	NumberOfResults int             `json:"number_of_results"`
	Results         []SearchResults `json:"results,omitempty"`
	Suggestions     []string        `json:"suggestions,omitempty"`
}

type Event struct {
	Event string         `json:"event"`
	ID    string         `json:"id"`
	Retry int            `json:"retry"`
	Data  map[string]any `json:"data"`
}

type ErrorReport struct {
	Service   string `json:"service"`
	Endpoint  string `json:"endpoint"`
	Error     string `json:"error"`
	Traceback string `json:"traceback,omitempty"`
}

type RequestError struct {
	StatusCode int
	Err        error
}

func (r *RequestError) Error() string {
	return fmt.Sprintf("status %d: err %v", r.StatusCode, r.Err)
}

type UserMetadata struct {
	Email          string `json:"email,omitempty"`
	UserID         uint64 `json:"user_id,omitempty"`
	Credits        uint64 `json:"credits,omitempty"`
	PlanRequests   uint   `json:"plan_requests,omitempty"`
	AllowOverspend bool   `json:"allow_overspend,omitempty"`
	StoreData      bool   `json:"store_data,omitempty"`
	Role           string `json:"role,omitempty"`
	APIKey         string
}

type Endpoints struct {
	CHAT       string
	COMPLETION string
	EMBEDDING  string
	RESPONSES  string
}

var ENDPOINTS = Endpoints{CHAT: "CHAT", COMPLETION: "COMPLETION", EMBEDDING: "EMBEDDING", RESPONSES: "RESPONSES"}

var ROUTES = map[string]string{
	ENDPOINTS.CHAT:       "/v1/chat/completions",
	ENDPOINTS.COMPLETION: "/v1/completions",
	ENDPOINTS.EMBEDDING:  "/v1/embeddings",
	ENDPOINTS.RESPONSES:  "/v1/responses",
}

type RequestInfo struct {
	Body      []byte
	UserID    uint64
	Credits   uint64
	StoreData bool
	ID        string
	StartTime time.Time
	Endpoint  string
	Model     string
	Stream    bool
	URL       string
}

type ProcessedQueryInfo struct {
	CreatedAt        time.Time
	UserID           uint64
	Model            string
	ModelID          uint64
	Endpoint         string
	TotalTime        time.Duration
	TimeToFirstToken time.Duration
	Usage            *Usage
	Cost             ResponseInfoCost
	TotalCredits     uint64
	ID               string
	ResponseContent  string
	RequestContent   []byte
}

// Usage tracks token usage for API requests
type Usage struct {
	PromptTokens     uint64
	CompletionTokens uint64
	TotalTokens      uint64
	IsCanceled       bool
}

// ResponseInfo contains information about the completed request
type ResponseInfo struct {
	ModelID          uint64
	Completed        bool
	Canceled         bool
	TotalTime        time.Duration
	TimeToFirstToken time.Duration
	Usage            *Usage
	ResponseContent  string
	Cost             ResponseInfoCost
}
type ResponseInfoCost struct {
	InputCredits    uint64
	OutputCredits   uint64
	CanceledCredits uint64
}

type OpenAIError struct {
	Message string `json:"message"`
	Object  string `json:"object"`
	Type    string `json:"Type"`
	Code    int    `json:"code"`
}

const CreditsToUSD = 0.00000001
