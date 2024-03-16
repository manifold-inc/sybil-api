package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	"github.com/labstack/echo/v4"
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

func querySerper(query string, endpoint string) (map[string]any, error) {
	SERPER_KEY := safeEnv("SERPER_KEY")
	body, _ := json.Marshal(map[string]string{
		"q": query,
	})
	r, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(body))
	if err != nil {
		log.Printf("Serper Error: %s", err.Error())
		return nil, err
	}
	defer r.Body.Close()
	r.Header["Content-Type"] = []string{"application/json"}
	r.Header["X-API-KEY"] = []string{SERPER_KEY}
	client := &http.Client{}
	res, err := client.Do(r)
	if err != nil {
		log.Printf("Serper Error: %s", err.Error())
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		log.Printf("Serper Error. Status code: %d", res.StatusCode)
		return nil, err
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

func sendEvent(c echo.Context, data any) {
	eventId := uuid.New().String()
	fmt.Fprintf(c.Response(), "id: %s\n", eventId)
	fmt.Fprintf(c.Response(), "event: new_message\n")
	eventData, _ := json.Marshal(data)
	fmt.Fprintf(c.Response(), "data: %s\n", string(eventData))
	fmt.Fprintf(c.Response(), "retry: %d\n\n", 1500)
	c.Response().Flush()
}

func querySearch(wg *sync.WaitGroup, c echo.Context, query string, src chan []string) {
	defer wg.Done()
	search, err := querySerper(query, SEARCH)
	if err != nil {
		close(src)
		return
	}
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
		sources = append(sources, SerperResults{
			Type:      "url",
			Url:       element["link"].(string),
			Snippet:   element["snippet"].(string),
			Title:     element["title"].(string),
			Icon:      icon,
			Published: date,
		})
	}
	sendEvent(c, map[string]any{
		"type":    "sources",
		"sources": sources,
	})
	var llmSources []string
	for _, element := range sources {
		llmSources = append(llmSources, fmt.Sprintf("Title: %s:\nSnippet: %s", element.Title, element.Snippet))
	}
	src <- llmSources
	src <- llmSources

	relatedSearches, ok := search["relatedSearches"]
	if !ok {
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

func queryNews(wg *sync.WaitGroup, c echo.Context, query string) {
	defer wg.Done()
	newsResults, err := querySerper(query, NEWS)
	if err != nil {
		return
	}
	news := newsResults["news"].([]interface{})
	if len(news) > 0 {
		herocard := news[0].(map[string]interface{})
		link, ok := herocard["link"]
		if !ok {
			return
		}
		sendEvent(c, map[string]any{
			"type": "heroCard",
			"heroCard": map[string]string{
				"type":  "news",
				"url":   link.(string),
				"image": herocard["imageUrl"].(string),
				"title": herocard["title"].(string),
				"intro": herocard["snippet"].(string),
				"size":  "auto",
			},
		})
	}
}
func queryImages(wg *sync.WaitGroup, c echo.Context, query string) {
	defer wg.Done()
	imageResults, err := querySerper(query, IMAGE)
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

func queryMiners(wg *sync.WaitGroup, c echo.Context, client *redis.Client, sources chan []string, query string, answer chan string) {
	defer close(answer)
	defer wg.Done()
	ctx := context.Background()
	defer ctx.Done()
	rh := rejson.NewReJSONHandler()
	rh.SetGoRedisClientWithContext(ctx, client)
	minerJSON, err := rh.JSONGet("miners", ".")
	if err != nil {
		log.Printf("Failed to JSONGet: %s", err.Error())
		return
	}

	var minerOut []Miner
	err = json.Unmarshal(minerJSON.([]byte), &minerOut)
	if err != nil {
		log.Printf("Failed to JSON Unmarshal: %s", err.Error())
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

	response := make(chan *http.Response)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	minersLeft := len(minerOut)
	var minerWaitGroup sync.WaitGroup
	minerWaitGroup.Add(len(minerOut))
	go func() {
		minerWaitGroup.Wait()
		close(response)
	}()
	for _, m := range minerOut {
		go func(miner Miner) {
			defer func() { minersLeft = minersLeft - 1 }()
			defer minerWaitGroup.Done()
			message := []string{fmt.Sprint(nonce), HOTKEY, miner.Hotkey, INSTANCE_UUID, bodyHash}
			joinedMessage := strings.Join(message, ".")
			signedMessage := signMessage(joinedMessage, PUBLIC_KEY, PRIVATE_KEY)
			port := fmt.Sprint(miner.Port)
			version := 670
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
					Ip:            IP,
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

			httpClient := http.Client{}
			endpoint := "http://" + miner.Ip + ":" + fmt.Sprint(miner.Port) + "/Inference"
			out, err := json.Marshal(body)
			r, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(out))
			if err != nil {
				log.Printf("Failed miner request: %s\n", err.Error())
				return
			}

			r.Header["Content-Type"] = []string{"application/json"}
			r.Header["name"] = []string{"Inference"}
			r.Header["timeout"] = []string{"12.0"}
			r.Header["bt_header_axon_ip"] = []string{miner.Ip}
			r.Header["bt_header_axon_port"] = []string{strconv.Itoa(miner.Port)}
			r.Header["bt_header_axon_hotkey"] = []string{miner.Hotkey}
			r.Header["bt_header_dendrite_ip"] = []string{IP}
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
				log.Printf("Miner: %s %s\nError: %s", miner.Hotkey, miner.Coldkey, err.Error())
				res.Body.Close()
				return
			}
			if res.StatusCode != http.StatusOK {
				bdy, _ := io.ReadAll(res.Body)
				res.Body.Close()
				log.Printf("Miner: %s %s\nError: %s", miner.Hotkey, miner.Coldkey, string(bdy))
				return
			}
			axon_version := res.Header.Get("Bt_header_axon_version")
			ver, err := strconv.Atoi(axon_version)
			if err != nil || ver < 672 {
				bdy, _ := io.ReadAll(res.Body)
				res.Body.Close()
				log.Printf("Miner: %s %s\nError: %s", miner.Hotkey, miner.Coldkey, string(bdy))
				return
			}
			response <- res
		}(m)
	}

	for {
		res, ok := <-response
		if !ok {
			c.Response().WriteHeader(http.StatusInternalServerError)
			return
		}
		defer res.Body.Close()
		reader := bufio.NewReader(res.Body)
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
			res.Body.Close()
		}
		if response == nil {
			break
		}
	}
}

func saveAnswer(c echo.Context, query string, answer chan string, sources chan []string, session string) {
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

	ans, more := <-answer
	if !more {
		log.Println("Faield to get answer")
		return
	}
	srcs, more := <-sources
	if !more {
		log.Println("Faield to get sources")
		return
	}
	jsonSrcs, _ := json.Marshal(srcs)
	q := `INSERT INTO search (public_id, user_iid, query, sources, completion) VALUES (?, ?, ?, ?, ?);`
	_, err = db.Exec(q, publicId, userId, query, string(jsonSrcs), ans)
	if err != nil && err != sql.ErrNoRows {
		log.Println("Insert Search Error:", err)
		return
	}
	log.Println("Inserted into db")
}
