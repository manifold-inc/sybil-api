package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
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

func getEnv(env, fallback string) string {
	if value, ok := os.LookupEnv(env); ok {
		return value
	}
	return fallback
}

func queryGoogleSearch(c *Context, query string, page int, searchType ...string) (*SearchResponseBody, error) {
	search := googleService.Cse.List().Q(query).Cx(GOOGLE_SEARCH_ENGINE_ID)

	if len(searchType) > 0 && searchType[0] == "image" {
		search = search.SearchType("image")
	}

	if page > 1 {
		search = search.Start(int64(page-1)*10 + 1)
	}

	res, err := search.Do()
	if err != nil {
		c.Err.Printf("Google Search Error: %s", err.Error())
		return nil, err
	}

	results := make([]SearchResults, len(res.Items))
	for i, item := range res.Items {
		title := item.Title
		content := item.Snippet
		link := item.Link
		imgSource := link // Default to link if no specific image source found
		source := ""
		resolution := ""
		metadata := ""
		publishedDate := ""

		// Handle pagemap for image source and metadata if available
		if item.Pagemap != nil {
			var pagemap map[string]interface{}
			if err := json.Unmarshal(item.Pagemap, &pagemap); err != nil {
				c.Err.Printf("Failed to unmarshal pagemap: %s", err.Error())
				continue
			}

			// Helper function to safely extract string from map
			getString := func(m map[string]interface{}, key string) string {
				if val, ok := m[key].(string); ok {
					return val
				}
				return ""
			}

			// Helper function to safely extract map from array
			getFirstMap := func(arr []interface{}) map[string]interface{} {
				if len(arr) > 0 {
					if m, ok := arr[0].(map[string]interface{}); ok {
						return m
					}
				}
				return nil
			}

			// Helper function to extract image data from a map
			extractImageData := func(m map[string]interface{}, srcKey string) (string, string) {
				src := getString(m, srcKey)
				width := getString(m, "width")
				height := getString(m, "height")
				if width != "" && height != "" {
					return src, fmt.Sprintf("%sx%s", width, height)
				}
				return src, ""
			}

			// Handle image search results
			if len(searchType) > 0 && searchType[0] == "image" {
				// Try cse_image first
				if cseImages, ok := pagemap["cse_image"].([]interface{}); ok {
					if cseImage := getFirstMap(cseImages); cseImage != nil {
						src, res := extractImageData(cseImage, "src")
						if src != "" {
							imgSource = src
						}
						if res != "" {
							resolution = res
						}
					}
				}

				// Try imageobject for additional metadata
				if imageObjects, ok := pagemap["imageobject"].([]interface{}); ok {
					if imageObject := getFirstMap(imageObjects); imageObject != nil {
						src, res := extractImageData(imageObject, "url")
						if src != "" {
							imgSource = src
						}
						if res != "" {
							resolution = res
						}
						if content := getString(imageObject, "content"); content != "" {
							metadata = content
						}
					}
				}
			}

			// Handle metadata from metatags
			if metatags, ok := pagemap["metatags"].([]interface{}); ok {
				if metatag := getFirstMap(metatags); metatag != nil {
					publishedDate = getString(metatag, "article:published_time")
					if desc := getString(metatag, "og:description"); desc != "" {
						metadata = desc
					}
				}
			}
		}

		// Get source and parsed URL from link
		if link != "" {
			if parsed, err := url.Parse(link); err == nil {
				source = parsed.Hostname()
				parsedUrl := strings.Split(source, ".")
				results[i] = SearchResults{
					Title:         &title,
					Content:       &content,
					Url:           &link,
					ImgSource:     &imgSource,
					ParsedUrl:     &parsedUrl,
					Source:        &source,
					Resolution:    &resolution,
					Metadata:      &metadata,
					PublishedDate: &publishedDate,
				}
				continue
			}
		}

		// Fallback for when URL parsing fails
		emptyParsedUrl := []string{}
		results[i] = SearchResults{
			Title:         &title,
			Content:       &content,
			Url:           &link,
			ImgSource:     &imgSource,
			ParsedUrl:     &emptyParsedUrl,
			Source:        &source,
			Resolution:    &resolution,
			Metadata:      &metadata,
			PublishedDate: &publishedDate,
		}
	}

	// Create and return SearchResponseBody
	totalResults, err := strconv.Atoi(res.SearchInformation.TotalResults)
	if err != nil {
		c.Err.Printf("Error converting total results to int: %s", err.Error())
		totalResults = 0
	}

	// Get related queries
	suggestions := []string{}
	relatedSearch := googleService.Cse.List().Q("related:" + query).Cx(GOOGLE_SEARCH_ENGINE_ID)
	relatedRes, err := relatedSearch.Do()
	if err == nil && relatedRes.Items != nil {
		for _, item := range relatedRes.Items {
			if item.Title != "" {
				suggestions = append(suggestions, item.Title)
			}
		}
	}

	return &SearchResponseBody{
		Query:             query,
		Number_of_results: totalResults,
		Results:           results,
		Suggestions:       suggestions,
	}, nil
}

func queryGoogleAutocomplete(c *Context, query string) ([]interface{}, error) {
	// Google's autocomplete endpoint
	url := fmt.Sprintf("https://suggestqueries.google.com/complete/search?client=firefox&q=%s", url.QueryEscape(query))

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		c.Err.Printf("Failed to create autocomplete request: %s", err.Error())
		return nil, err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		c.Err.Printf("Autocomplete Error: %s", err.Error())
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		c.Err.Printf("Autocomplete Error. Status code: %d", res.StatusCode)
		return nil, fmt.Errorf("autocomplete failed with status: %d", res.StatusCode)
	}

	// Google's response is in JSON format: [query, [suggestions], [], metadata]
	var response []interface{}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		c.Err.Printf("Failed to decode autocomplete response: %s", err.Error())
		return nil, err
	}

	// Return only the first two elements: [query, [suggestions]]
	if len(response) >= 2 {
		return []interface{}{response[0], response[1]}, nil
	}

	// If response is malformed, return empty query and suggestions
	return []interface{}{"", []string{}}, nil
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

func queryFallbacks(c *Context, sources []string, query string, model string) string {
	tr := &http.Transport{
		MaxIdleConns:      10,
		IdleConnTimeout:   30 * time.Second,
		DisableKeepAlives: false,
	}

	httpClient := http.Client{Transport: tr, Timeout: 2 * time.Minute}

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
	`, now.Format("Mon Jan2 15:04:05 MST 2006"), sources_string)}, {Role: "user", Content: query}}

	body := InferenceBody{
		Messages:    messages,
		MaxTokens:   3012,
		Temperature: .3,
		Stream:      true,
		Model:       model,
	}

	endpoint := "http://" + safeEnv("FALLBACK_SERVER") + "/v1/chat/completions"
	out, err := json.Marshal(body)
	if err != nil {
		c.Warn.Printf("Failed to parse json %s", err.Error())
		return ""
	}

	headers := map[string]string{
		"X-Targon-Model": model,
		"Authorization":  fmt.Sprintf("Bearer %s", safeEnv("FALLBACK_SERVER_API_KEY")),
		"Content-Type":   "application/json",
		"Connection":     "keep-alive",
	}

	r, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(out))
	if err != nil {
		c.Warn.Printf("Failed fallback request: %s", err.Error())
		return ""
	}

	for key, value := range headers {
		r.Header.Set(key, value)
	}
	r.Close = true

	res, err := httpClient.Do(r)
	if err != nil {
		c.Warn.Printf("Failed fallback request\nError: %s\n", err.Error())
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.Warn.Printf("Failed fallback request\nError: %d\n", res.StatusCode)
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

/*
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
*/

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
