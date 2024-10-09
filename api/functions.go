package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aidarkhanov/nanoid"
	"github.com/google/uuid"
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

func querySearx(c *Context, query string, categories string, page int) (*SearxResponseBody, error) {
	res, err := http.PostForm(SEARX_URL+"/search", url.Values{
		"q":          {query},
		"format":     {"json"},
		"pageno":     {fmt.Sprint(page)},
		"categories": {categories},
	})

	if err != nil {
		c.Err.Printf("Search Error: %s\n", err.Error())
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.Err.Printf("Search Error. Status code: %d\n", res.StatusCode)
		return nil, errors.New("Search Failed")
	}

	var resp SearxResponseBody
	json.NewDecoder(res.Body).Decode(&resp)
	return &resp, nil
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

	httpClient := http.Client{Transport: tr, Timeout: 10 * time.Second}

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
		Model:       "NousResearch/Meta-Llama-3.1-8B-Instruct",
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
			c.Info.Println(content)
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
