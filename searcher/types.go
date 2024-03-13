package main

type Miner struct {
	Ip     string `json:"ip,omitempty"`
	Port   int    `json:"port,omitempty"`
	Hotkey string `json:"hotkey,omitempty"`
}

type RequestBody struct {
	Query string   `json:"query"`
	Files []string `json:"files"`
}

type SearchBody struct {
	Name           string         `json:"name"`
	Timeout        float32        `json:"timeout"`
	TotalSize      int            `json:"total_size"`
	HeaderSize     int            `json:"header_size"`
	Dendrite       DendriteOrAxon `json:"dendrite"`
	Axon           DendriteOrAxon `json:"axon"`
	BodyHash       string         `json:"body_hash"`
	RequiredFields []string       `json:"required_hash_fields"`
	Sources        []string       `json:"sources"`
	Query          string         `json:"query"`
	SamplingParams SamplingParams `json:"sampling_params"`
	Completion     *string        `json:"completion"`
}

type DendriteOrAxon struct {
	StatusCode    *string `json:"status_code"`
	StatusMessage *string `json:"status_message"`
	ProcessTime   *string `json:"process_time"`
	Ip            string  `json:"ip"`
	Port          *string `json:"port"`
	Version       *int    `json:"version"`
	Nonce         *int64  `json:"nonce"`
	Uuid          *string `json:"uuid"`
	Hotkey        string  `json:"hotkey"`
	Signature     *string `json:"signature"`
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
	Seed                *string  `json:"seed"`
	Truncate            *string  `json:"truncate"`
}

type SerperResults struct {
	Type      string `json:"type"`
	Url       string `json:"url"`
	Snippet   string `json:"snippet"`
	Title     string `json:"title"`
	Icon      string `json:"icon"`
	Published string `json:"published"`
}
type Event struct {
	Event string                 `json:"event"`
	Id    string                 `json:"id"`
	Retry int                    `json:"retry"`
	Data  map[string]interface{} `json:"data"`
}
