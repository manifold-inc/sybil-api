package main

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type Response struct {
	Id      string   `json:"id"`
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

type Event struct {
	Event string                 `json:"event"`
	Id    string                 `json:"id"`
	Retry int                    `json:"retry"`
	Data  map[string]interface{} `json:"data"`
}

type ErrorReport struct {
	Service   string `json:"service"`
	Endpoint  string `json:"endpoint"`
	Error     string `json:"error"`
	Traceback string `json:"traceback,omitempty"`
}


type UrlSource struct {
	URL           string   `json:"url"`
	Thumbnail     string  `json:"thumbnail,omitempty"`
	Title         string   `json:"title"`
	ParsedURL     []string `json:"parsed_url"`
	Content       string  `json:"content,omitempty"`
	PublishedDate *string  `json:"publishedDate,omitempty"`
}
