package shared

import (
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
}

type ChatSettings struct {
	Model             string   `json:"model"`
	MaxTokens         int      `json:"max_tokens"`
	Temperature       float32  `json:"temperature"`
	TopP              float32  `json:"top_p"`
	TopK              float32  `json:"top_k"`
	FrequencyPenalty  float32  `json:"frequency_penalty"`
	PresencePenalty   float32  `json:"presence_penalty"`
	RepetitionPenalty float32  `json:"repetition_penalty"`
	Stop              []string `json:"stop"`
	Seed              int      `json:"seed"`
	Stream            bool     `json:"stream"`
	Logprobs          bool     `json:"logprobs"`
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

type ProcessedQueryInfo struct {
	CreatedAt        time.Time
	UserID           uint64
	Model            string
	ModelID          uint64
	Endpoint         string
	TotalTime        time.Duration
	TimeToFirstToken time.Duration
	Usage            *Usage
	TotalCredits     uint64
}

// Usage tracks token usage for API requests
type Usage struct {
	PromptTokens     uint64
	CompletionTokens uint64
	TotalTokens      uint64
	IsCanceled       bool
}

type OpenAIError struct {
	Message string `json:"message"`
	Object  string `json:"object"`
	Type    string `json:"Type"`
	Code    int    `json:"code"`
}

const CreditsToUSD = 0.00000001
