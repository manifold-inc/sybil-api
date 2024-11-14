package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aidarkhanov/nanoid"
	"github.com/google/uuid"
	brave "dev.freespoke.com/brave-search"
)

func derefString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

func safeEnv(env string) string {
	// Lookup env variable, and panic if not present

	res, present := os.LookupEnv(env)
	if !present {
		log.Fatalf("Missing environment variable %s", env)
	}
	return res
}

func QueryBraveWeb(cc *Context, query string, vertical string, page int) (*brave.WebSearchResult, error) {
	var res *brave.WebSearchResult
	if vertical == "images" {
		return nil, fmt.Errorf("images category not supported in web search")
	}
	res, err := brave_client.WebSearch(cc.Request().Context(), query)

	if err != nil {
		cc.Err.Printf("Failed to query brave: %s\n", err.Error())
		return nil, err
	}

	return res, nil
}

func QueryBraveImages(cc *Context, query string, page int) (*brave.ImageSearchResult, error) {
	client, err := brave.New(safeEnv("BRAVE_API_KEY"))
	if err != nil {
		cc.Err.Printf("Failed to create brave client: %s\n", err.Error())
		return nil, err
	}
	res, err := client.ImageSearch(cc.Request().Context(), query)
	if err != nil {
		cc.Err.Printf("Failed to query brave: %s\n", err.Error())
		return nil, err
	}
	return res, nil
}


func sendEvent(c *Context, data map[string]any) {
	// Send SSE event to response

	eventId := uuid.New().String()
	fmt.Fprintf(c.Response(), "id: %s\n", eventId)
	fmt.Fprintf(c.Response(), "event: new_message\n")
	eventData, _ := json.Marshal(data)
	fmt.Fprintf(c.Response(), "data: %s\n", string(eventData))
	fmt.Fprintf(c.Response(), "retry: %d\n\n", 1500)
	c.Response().Flush()
}

func queryTargon(c *Context, sources []string, query string) string {
	tr := &http.Transport{
		MaxIdleConns:      10,
		IdleConnTimeout:   30 * time.Second,
		DisableKeepAlives: false,
	}

	httpClient := http.Client{Transport: tr, Timeout: 15 * time.Second}

	now := time.Now()
	sources_string := ""
	for i := range sources {
		sources_string += sources[i] + "\n"
	}
	messages := []ChatMessage{{Role: "system", Content: fmt.Sprintf(`### Current Date: %s
	### Instruction: 
	You are Sybil.com, an expert language model tasked with performing a search over the given query and search results.
	You are running the text generation on Subnet 4, a bittensor subnet developed by Manifold Labs.
	Your answer should be short, two paragraphs exactly, and should be relevant to the query.

	### Sources:
	%s
	`, now.Format("Mon Jan 2 15:04:05 MST 2006"), sources_string)}, {Role: "user", Content: query}}

	body := InferenceBody{
		Messages:    messages,
		MaxTokens:   3012,
		Temperature: .3,
		Stream:      true,
		Logprobs:    true,
		Model:       "nvidia/Llama-3.1-Nemotron-70B-Instruct-HF",
	}

	endpoint := TARGON_HUB_ENDPOINT + "/chat/completions"
	out, err := json.Marshal(body)
	if err != nil {
		c.Warn.Printf("Failed to parse json %s", err.Error())
		return ""
	}

	headers := map[string]string{
		"Authorization": "Bearer " + TARGON_HUB_ENDPOINT_API_KEY,
	}

	r, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(out))
	if err != nil {
		c.Warn.Printf("Failed targon request: %s\n", err.Error())
		return ""
	}

	// Set headers
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	r.Close = true

	res, err := httpClient.Do(r)
	if err != nil {
		c.Warn.Printf("Failed targon hub request\nError: %s\n", err.Error())
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.Warn.Printf("Failed targon hub request\nError: %d\n", res.StatusCode)
		return ""
	}

	reader := bufio.NewScanner(res.Body)
	finished := false
	tokens := 0
	responseText := ""
	var response Response
	for reader.Scan() {
		select {
		case <-c.Request().Context().Done():
			return ""
		default:
			token := reader.Text()
			if token == "data: [DONE]" {
				finished = true
				sendEvent(c, map[string]any{
					"type":     "answer",
					"text":     "",
					"finished": finished,
				})
				return responseText
			}
			token, found := strings.CutPrefix(token, "data: ")
			if !found {
				continue
			}
			tokens += 1
			err := json.Unmarshal([]byte(token), &response)
			if err != nil {
				c.Err.Printf("Failed decoding token string: %s", err)
				continue
			}
			content := response.Choices[0].Delta.Content
			sendEvent(c, map[string]any{
				"type":     "answer",
				"text":     content,
				"finished": finished,
			})
			responseText += content
		}
	}
	if finished == false {
		return ""
	}
	return responseText
}

func saveAnswer(query string, answer string, sources []string, session string) {
	if DEBUG {
		return
	}
	publicId, err := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 29)
	if err != nil {
		log.Println("Failed generating publicId for db:", err)
		return
	}
	publicId = "sh_" + publicId
	var nonNullUserId int
	userId := &nonNullUserId
	userId = nil
	if len(session) > 0 {
		err := db.QueryRow(`
			SELECT user.iid 
			FROM session 
			JOIN user ON user.id = session.user_id 
			WHERE session.id = ? 
			AND session.expires_at > CURRENT_TIMESTAMP()
		`, session).Scan(&userId)
		if err != nil && err != sql.ErrNoRows {
			log.Println("Get userId Error: ", err)
			return
		}
	}

	jsonSrcs, _ := json.Marshal(sources)
	q := `INSERT INTO search (public_id, user_iid, query, sources, completion) VALUES (?, ?, ?, ?, ?);`
	_, err = db.Exec(q, publicId, userId, query, string(jsonSrcs), answer)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("Error: %s\n", err)
		return
	}
	log.Println("Inserted Results")
}

func sendErrorToEndon(err error, endpoint string) {
	payload := ErrorReport{
		Service:  "sybil-api",
		Endpoint: endpoint,
		Error:    err.Error(),
	}

	jsonData, jsonErr := json.Marshal(payload)
	if jsonErr != nil {
		log.Printf("Failed to marshal error payload: %v\n", jsonErr)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, reqErr := http.NewRequest(http.MethodPost, ENDON_URL, bytes.NewBuffer(jsonData))
	if reqErr != nil {
		log.Printf("Failed to create Endon request: %v\n", reqErr)
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, respErr := client.Do(req)
	if respErr != nil {
		log.Printf("Failed to send error to Endon: %v\n", respErr)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("Failed to report error to Endon. Status: %d\n", resp.StatusCode)
		return
	}

	fmt.Printf("Successfully sent error to Endon: %s\n", endpoint)
}
