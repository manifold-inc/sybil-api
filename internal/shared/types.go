package shared

import "fmt"

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
	Delta Delta `json:"delta"`
}
type Delta struct {
	Content string `json:"content"`
}

type InferenceBody struct {
	Messages    []ChatMessage `json:"messages"`
	Temperature float32       `json:"temperature"`
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	Stream      bool          `json:"stream"`
	Logprobs    bool          `json:"logprobs"`
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
	PlanID         uint64 `json:"plan_id,omitempty"`
	PlanCredits    uint64 `json:"plan_credits,omitempty"`
	BoughtCredits  uint64 `json:"bought_credits,omitempty"`
	AllowOverspend bool   `json:"allow_overspend,omitempty"`
	RPM            int    `json:"rpm,omitempty"`
	StoreData      bool   `json:"store_data,omitempty"`
	APIKey         string
}
