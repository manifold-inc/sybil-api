package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChainSafe/go-schnorrkel"
	"github.com/aidarkhanov/nanoid"
	"github.com/google/uuid"
	"github.com/nitishm/go-rejson/v4"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/sha3"
)

func safeEnv(env string) string {
	res, present := os.LookupEnv(env)
	if !present {
		log.Fatalf("Missing environment variable %s", env)
	}
	return res
}

func signMessage(message string, public string, private string) string {
	var pubk [32]byte
	data, err := hex.DecodeString(public)
	if err != nil {
		log.Fatalf("Failed to decode public key: %s", err)
	}
	copy(pubk[:], data)

	var prik [32]byte
	data, err = hex.DecodeString(private)
	if err != nil {
		log.Fatalf("Failed to decode private key: %s", err)
	}
	copy(prik[:], data)

	msg := []byte(message)
	priv := schnorrkel.SecretKey{}
	priv.Decode(prik)
	pub := schnorrkel.PublicKey{}
	pub.Decode(pubk)
	signingCtx := []byte("substrate")
	signingTranscript := schnorrkel.NewSigningContext(signingCtx, msg)
	sig, _ := priv.Sign(signingTranscript)
	sigEncode := sig.Encode()
	out := hex.EncodeToString(sigEncode[:])
	return "0x" + out
}

func querySerper(c *Context, query string, endpoint string, page int) (map[string]any, error) {
	SERPER_KEY := safeEnv("SERPER_KEY")
	body, _ := json.Marshal(map[string]interface{}{
		"q":    query,
		"page": page,
	})
	r, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(body))
	if err != nil {
		log.Printf("Serper Error: %s\n", err.Error())
		return nil, err
	}
	defer r.Body.Close()
	r.Header["Content-Type"] = []string{"application/json"}
	r.Header["X-API-KEY"] = []string{SERPER_KEY}
	client := &http.Client{}
	res, err := client.Do(r)
	if err != nil {
		c.Err.Printf("Serper Error: %s\n", err.Error())
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.Err.Printf("Serper Error. Status code: %d\n", res.StatusCode)
		return nil, errors.New("Serper Failed")
	}
	resp := map[string]any{}
	json.NewDecoder(res.Body).Decode(&resp)
	return resp, nil
}

func hashString(str string) string {
	h := sha3.New256()
	h.Write([]byte(str))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}

func formatListToPythonString(list []string) string {
	strList := "["
	for i, element := range list {
		element = strconv.Quote(element)
		element = strings.TrimPrefix(element, "\"")
		element = strings.TrimSuffix(element, "\"")
		separator := "'"
		if strings.ContainsRune(element, '\'') && !strings.ContainsRune(element, '"') {
			separator = "\""
		} else {
			element = strings.ReplaceAll(element, "'", "\\'")
			element = strings.ReplaceAll(element, "\\\"", "\"")
		}
		if i != 0 {
			strList += ", "
		}
		strList += separator + element + separator
	}
	strList += "]"
	return strList
}

func sendEvent(c *Context, data any) {
	eventId := uuid.New().String()
	fmt.Fprintf(c.Response(), "id: %s\n", eventId)
	fmt.Fprintf(c.Response(), "event: new_message\n")
	eventData, _ := json.Marshal(data)
	fmt.Fprintf(c.Response(), "data: %s\n", string(eventData))
	fmt.Fprintf(c.Response(), "retry: %d\n\n", 1500)
	c.Response().Flush()
}

func parseSources(search map[string]any) []SerperResults {
	organic_results := search["organic"].([]interface{})
	sources := []SerperResults{}
	for _, element := range organic_results {
		element := element.(map[string]interface{})
		url, err := url.Parse(element["link"].(string))
		icon := "https://www.micreate.eu/wp-content/img/default-img.png"
		if err == nil {
			hostname := strings.TrimPrefix(url.Hostname(), "www.")
			icon = fmt.Sprintf("https://www.google.com/s2/favicons?domain=%s&sz=8", hostname)
		}
		date, ok := element["date"].(string)
		if !ok {
			date = ""
		}
		snippet, ok := element["snippet"].(string)
		if !ok {
			snippet = ""
		}
		title, ok := element["title"].(string)
		if !ok {
			title = ""
		}
		link, ok := element["link"].(string)
		if !ok {
			link = ""
		}
		sources = append(sources, SerperResults{
			Type:      "url",
			Url:       link,
			Snippet:   snippet,
			Title:     title,
			Icon:      icon,
			Published: date,
		})
	}
	return sources
}

func querySearch(wg *sync.WaitGroup, c *Context, query string, src chan []string, page int) {
	defer wg.Done()
	search, err := querySerper(c, query, SEARCH, page)
	if err != nil {
		close(src)
		return
	}
	sources := parseSources(search)

	sendEvent(c, map[string]any{
		"type":    "sources",
		"sources": sources,
	})
	var llmSources []string
	for _, element := range sources {
		llmSources = append(llmSources, fmt.Sprintf("Title: %s:\nSnippet: %s\n", element.Title, element.Snippet))
	}
	src <- llmSources
	src <- llmSources

	relatedSearches, ok := search["relatedSearches"]
	if !ok {
		sendEvent(c, map[string]any{
			"type":      "related",
			"followups": []string{},
		})
		return
	}
	var relatedList []string
	for _, element := range relatedSearches.([]interface{}) {
		relatedList = append(relatedList, element.(map[string]interface{})["query"].(string))
	}
	sendEvent(c, map[string]any{
		"type":      "related",
		"followups": relatedList,
	})
}

func queryNews(wg *sync.WaitGroup, c *Context, query string) {
	defer wg.Done()
	newsResults, err := querySerper(c, query, NEWS, 1)
	if err != nil {
		return
	}
	news := newsResults["news"].([]interface{})
	if len(news) > 0 {
		herocard := news[0].(map[string]interface{})
		link, ok := herocard["link"]
		if !ok {
			sendEvent(c, map[string]any{
				"type":     "heroCard",
				"heroCard": nil,
			})
			return
		}
		image, ok := herocard["imageUrl"]
		if !ok {
			image = "https://img.freepik.com/premium-vector/beautiful-colorful-gradient-background_492281-1165.jpg"
		}
		snippet, ok := herocard["snippet"]
		if !ok {
			snippet = "Top Result from search"
		}
		title, ok := herocard["title"]
		if !ok {
			title = "Top Result"
		}
		sendEvent(c, map[string]any{
			"type": "heroCard",
			"heroCard": map[string]string{
				"type":  "news",
				"url":   link.(string),
				"image": image.(string),
				"title": title.(string),
				"intro": snippet.(string),
				"size":  "auto",
			},
		})
	}
}
func queryImages(wg *sync.WaitGroup, c *Context, query string) {
	defer wg.Done()
	imageResults, err := querySerper(c, query, IMAGE, 1)
	if err != nil {
		return
	}
	images := imageResults["images"].([]interface{})
	if len(images) > 0 {
		var results []map[string]interface{}
		for i, v := range images {
			if i == 4 {
				break
			}
			v := v.(map[string]interface{})
			results = append(results, map[string]interface{}{
				"type":    "image",
				"url":     v["imageUrl"].(string),
				"source":  v["link"],
				"version": 1,
				"size":    "auto",
			})
		}
		sendEvent(c, map[string]any{
			"type":  "cards",
			"cards": images,
		})
	}
}

func queryMiners(wg *sync.WaitGroup, c *Context, client *redis.Client, sources chan []string, query string, answer chan string) {
	defer close(answer)
	defer wg.Done()
	ctx := context.Background()
	defer ctx.Done()
	rh := rejson.NewReJSONHandler()
	rh.SetGoRedisClientWithContext(ctx, client)
	minerJSON, err := rh.JSONGet("miners", ".")
	if err != nil {
		c.Err.Printf("Failed to JSONGet: %s\n", err.Error())
		return
	}

	var minerOut []Miner
	err = json.Unmarshal(minerJSON.([]byte), &minerOut)
	if err != nil {
		c.Err.Printf("Failed to JSON Unmarshal: %s\n", err.Error())
		return
	}

	nonce := time.Now().UnixNano()

	llm_sources, more := <-sources
	if !more {
		return
	}
	formatted := hashString(formatListToPythonString(llm_sources))
	var hashes []string
	hashes = append(hashes, formatted)
	hashes = append(hashes, hashString(query))
	bodyHash := hashString(strings.Join(hashes, ""))

	type Response struct {
		Res     *http.Response
		ColdKey string
		HotKey  string
	}

	response := make(chan Response)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var minerWaitGroup sync.WaitGroup
	minerWaitGroup.Add(len(minerOut))
	go func() {
		minerWaitGroup.Wait()
		close(response)
	}()
	tr := &http.Transport{
		MaxIdleConns:      10,
		IdleConnTimeout:   30 * time.Second,
		DisableKeepAlives: false,
	}
	httpClient := http.Client{Transport: tr}
	for _, m := range minerOut {
		go func(miner Miner) {
			defer minerWaitGroup.Done()
			message := []string{fmt.Sprint(nonce), HOTKEY, miner.Hotkey, INSTANCE_UUID, bodyHash}
			joinedMessage := strings.Join(message, ".")
			signedMessage := signMessage(joinedMessage, PUBLIC_KEY, PRIVATE_KEY)
			port := fmt.Sprint(miner.Port)
			version := 672
			body := SearchBody{
				Name:           "Inference",
				Timeout:        12.0,
				TotalSize:      0,
				HeaderSize:     0,
				RequiredFields: []string{"sources", "query", "seed"},
				Sources:        llm_sources,
				Query:          query,
				BodyHash:       "",
				Dendrite: DendriteOrAxon{
					Ip:            "10.0.0.1",
					Version:       &version,
					Nonce:         &nonce,
					Uuid:          &INSTANCE_UUID,
					Hotkey:        HOTKEY,
					Signature:     &signedMessage,
					Port:          nil,
					StatusCode:    nil,
					StatusMessage: nil,
					ProcessTime:   nil,
				},
				Axon: DendriteOrAxon{
					StatusCode:    nil,
					StatusMessage: nil,
					ProcessTime:   nil,
					Version:       nil,
					Nonce:         nil,
					Uuid:          nil,
					Signature:     nil,
					Ip:            miner.Ip,
					Port:          &port,
					Hotkey:        miner.Hotkey,
				},
				SamplingParams: SamplingParams{
					Seed:                nil,
					Truncate:            nil,
					BestOf:              1,
					DecoderInputDetails: true,
					Details:             false,
					DoSample:            true,
					MaxNewTokens:        3072,
					RepetitionPenalty:   1.0,
					ReturnFullText:      false,
					Stop:                []string{"photographer"},
					Temperature:         .01,
					TopK:                10,
					TopNTokens:          5,
					TopP:                .9999999,
					TypicalP:            .9999999,
					Watermark:           false,
				},
				Completion: nil,
			}

			endpoint := "http://" + miner.Ip + ":" + fmt.Sprint(miner.Port) + "/Inference"
			out, err := json.Marshal(body)
			r, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(out))
			if err != nil {
				c.Warn.Printf("Failed miner request: %s\n", err.Error())
				return
			}
			r.Close = true
			r.Header["Content-Type"] = []string{"application/json"}
			r.Header["Connection"] = []string{"keep-alive"}
			r.Header["name"] = []string{"Inference"}
			r.Header["timeout"] = []string{"12.0"}
			r.Header["bt_header_axon_ip"] = []string{miner.Ip}
			r.Header["bt_header_axon_port"] = []string{strconv.Itoa(miner.Port)}
			r.Header["bt_header_axon_hotkey"] = []string{miner.Hotkey}
			r.Header["bt_header_dendrite_ip"] = []string{"10.0.0.1"}
			r.Header["bt_header_dendrite_version"] = []string{"672"}
			r.Header["bt_header_dendrite_nonce"] = []string{strconv.Itoa(int(nonce))}
			r.Header["bt_header_dendrite_uuid"] = []string{INSTANCE_UUID}
			r.Header["bt_header_dendrite_hotkey"] = []string{HOTKEY}
			r.Header["bt_header_input_obj_sources"] = []string{"W10="}
			r.Header["bt_header_input_obj_query"] = []string{"IiI="}
			r.Header["bt_header_dendrite_signature"] = []string{signedMessage}
			r.Header["header_size"] = []string{"0"}
			r.Header["total_size"] = []string{"0"}
			r.Header["computed_body_hash"] = []string{bodyHash}
			res, err := httpClient.Do(r)
			if err != nil {
				c.Warn.Printf("Miner: %s %s\nError: %s\n", miner.Hotkey, miner.Coldkey, err.Error())
				res.Body.Close()
				return
			}
			if res.StatusCode != http.StatusOK {
				bdy, _ := io.ReadAll(res.Body)
				res.Body.Close()
				c.Warn.Printf("Miner: %s %s\nError: %s\n", miner.Hotkey, miner.Coldkey, string(bdy))
				return
			}
			axon_version := res.Header.Get("Bt_header_axon_version")
			ver, err := strconv.Atoi(axon_version)
			if err != nil || ver < 672 {
				res.Body.Close()
				c.Warn.Printf("Miner: %s %s\nError: Axon version too low\n", miner.Hotkey, miner.Coldkey)
				return
			}
			response <- Response{Res: res, ColdKey: miner.Coldkey, HotKey: miner.Hotkey}
		}(m)
	}
	count := 0
	for {
		count++
		res, ok := <-response
		c.Info.Printf("Attempt: %d Miner: %s %s\n", count, res.HotKey, res.ColdKey)
		if !ok {
			return
		}
		reader := bufio.NewReader(res.Res.Body)
		finished := false
		ans := ""
		for {
			token, err := reader.ReadString(' ')
			if strings.Contains(token, "<s>") || strings.Contains(token, "</s>") || strings.Contains(token, "<im_end>") {
				finished = true
				token = strings.ReplaceAll(token, "<s>", "")
				token = strings.ReplaceAll(token, "</s>", "")
				token = strings.ReplaceAll(token, "<im_end>", "")
			}
			ans += token
			if err != nil && err != io.EOF {
				ans = ""
				c.Err.Println(err.Error())
				break
			}
			sendEvent(c, map[string]any{
				"type":     "answer",
				"text":     token,
				"finished": finished,
			})
			if err == io.EOF {
				break
			}
		}
		res.Res.Body.Close()
		if finished == false {
			continue
		}
		answer <- ans
		break
	}
	for {
		select {
		case res, ok := <-response:
			if !ok {
				response = nil
				break
			}
			res.Res.Body.Close()
		}
		if response == nil {
			break
		}
	}
}

func saveAnswer(c *Context, query string, answer chan string, sources chan []string, session string) {
	publicId, err := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 29)
	if err != nil {
		c.Err.Println("Failed generating publicId for db:", err)
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
			c.Warn.Println("Get userId Error: ", err)
			return
		}
	}

	ans, more := <-answer
	if !more {
		c.Warn.Println("Faield to get answer")
		return
	}
	srcs, more := <-sources
	if !more {
		c.Warn.Println("Faield to get sources")
		return
	}
	jsonSrcs, _ := json.Marshal(srcs)
	q := `INSERT INTO search (public_id, user_iid, query, sources, completion) VALUES (?, ?, ?, ?, ?);`
	_, err = db.Exec(q, publicId, userId, query, string(jsonSrcs), ans)
	if err != nil && err != sql.ErrNoRows {
		c.Err.Printf("Error: %s\n", err)
		return
	}
	c.Info.Println("Inserted Results")
}
