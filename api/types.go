package main

import "net/http"

type MinerResponse struct {
	Res     *http.Response
	ColdKey string
	HotKey  string
}

type Miner struct {
	Ip      string `json:"ip,omitempty"`
	Port    int    `json:"port,omitempty"`
	Hotkey  string `json:"hotkey,omitempty"`
	Coldkey string `json:"coldkey,omitempty"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type Epistula struct {
	Data      InferenceBody `json:"data"`
	Nonce     int64         `json:"nonce"`
	SignedBy  string        `json:"signed_by"`
	SignedFor string        `json:"signed_for"`
}

type InferenceBody struct {
	Messages       []ChatMessage  `json:"messages"`
	SamplingParams SamplingParams `json:"sampling_params"`
}

type SamplingParams struct {
	BestOf              int      `json:"best_of"`
	DecoderInputDetails bool     `json:"decoder_input_details"`
	Details             bool     `json:"details"`
	DoSample            bool     `json:"do_sample"`
	MaxNewTokens        int      `json:"max_new_tokens"`
	RepetitionPenalty   float32  `json:"repetition_penalty"`
	ReturnFullText      bool     `json:"return_full_text"`
	Stop                []string `json:"stop"`
	Temperature         float32  `json:"temperature"`
	TopK                int      `json:"top_k"`
	TopNTokens          int      `json:"top_n_tokens"`
	TopP                float32  `json:"top_p"`
	TypicalP            float32  `json:"typical_p"`
	Watermark           bool     `json:"watermark"`
	Seed                int      `json:"seed"`
	Truncate            *string  `json:"truncate"`
	Stream              bool     `json:"stream"`
}

type SearxResults struct {
	Url           *string   `json:"url,omitempty"`
	Source        *string   `json:"source,omitempty"`
	Resolution    *string   `json:"resolution,omitempty"`
	ImgSource     *string   `json:"img_src,omitempty"`
	Title         *string   `json:"title,omitempty"`
	Content       *string   `json:"content,omitempty"`
	Thumbnail     *string   `json:"thumbnail,omitempty"`
	ParsedUrl     *[]string `json:"parsed_url,omitempty"`
	Metadata      *string   `json:"metadata,omitempty"`
	PublishedDate *string   `json:"publishedDate,omitempty"`
}

type SearxResponseBody struct {
	Query             string         `json:"query"`
	Number_of_results int            `json:"number_of_results"`
	Results           []SearxResults `json:"results,omitempty"`
	Suggestions       []string       `json:"suggestions,omitempty"`
}

type Event struct {
	Event string                 `json:"event"`
	Id    string                 `json:"id"`
	Retry int                    `json:"retry"`
	Data  map[string]interface{} `json:"data"`
}
