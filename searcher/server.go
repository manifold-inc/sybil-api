package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/nitishm/go-rejson/v4"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/sha3"
)

func main() {
	HOTKEY := safeEnv("HOTKEY")
	IP := safeEnv("EXTERNAL_IP")
	PUBLIC_KEY := safeEnv("PUBLIC_KEY")
	PRIVATE_KEY := safeEnv("PRIVATE_KEY")
	NEWS := "https://google.serper.dev/news"
	SEARCH := "https://google.serper.dev/search"
	IMAGE := "https://google.serper.dev/images"
	// UUID for this instance. Persists through lifetime of service
	INSTANCE_UUID := uuid.New().String()

	e := echo.New()
	client := redis.NewClient(&redis.Options{
		Addr:     "cache:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	e.GET("/miners", func(c echo.Context) error {
		ctx := context.Background()
		defer ctx.Done()
		userSession := client.JSONGet(ctx, "miners").Val()
		fmt.Println(userSession)
		return c.JSON(200, userSession)
	})
	e.GET("/search", func(c echo.Context) (err error) {

		/*
			u := new(SearchBody)
			if err = c.Bind(u); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, err.Error())
			}
			if err = c.Validate(u); err != nil {
				return err
			}
		*/

		c.Response().Header().Set("Access-Control-Allow-Origin", "*")
		c.Response().Header().Set("Access-Control-Expose-Headers", "Content-Type")

		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")

		query := "How do you make cheese?" // TODO

		search := querySerper(query, SEARCH)
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

		var llm_sources []string
		for _, element := range sources {
			llm_sources = append(llm_sources, fmt.Sprintf("Title: %s:\nSnippet: %s", element.Title, element.Snippet))
		}

		news := querySerper(query, NEWS)["news"].([]interface{})
		if herocard := news[0].(map[string]interface{}); len(news) > 0 {
			sendEvent(c, map[string]any{
				"type": "heroCard",
				"heroCard": map[string]string{
					"type":  "news",
					"url":   herocard["link"].(string),
					"image": herocard["imageUrl"].(string),
					"title": herocard["title"].(string),
					"intro": herocard["snippet"].(string),
					"size":  "auto",
				},
			})
		}

		images := querySerper(query, IMAGE)["images"].([]interface{})
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

		//--------------------------
		// Query Miners
		//--------------------------
		ctx := context.Background()
		defer ctx.Done()
		rh := rejson.NewReJSONHandler()
		rh.SetGoRedisClientWithContext(ctx, client)
		minerJSON, err := rh.JSONGet("miners", ".")
		if err != nil {
			log.Fatalf("Failed to JSONGet: %s", err.Error())
		}

		var minerOut []Miner
		err = json.Unmarshal(minerJSON.([]byte), &minerOut)
		if err != nil {
			log.Fatalf("Failed to JSON Unmarshal: %s", err.Error())
		}
		firstMiner := minerOut[0]
		nonce := time.Now().UnixNano()

		var hashes []string

		formatted := formatListToPythonString(llm_sources)
		hashes = append(hashes, formatted)

		h := sha3.New256()
		h.Write([]byte(fmt.Sprint(query)))
		hashes = append(hashes, hex.EncodeToString(h.Sum(nil)))

		h = sha3.New256()
		h.Write([]byte(strings.Join(hashes, "")))
		bodyHash := hex.EncodeToString(h.Sum(nil))

		message := []string{fmt.Sprint(nonce), HOTKEY, firstMiner.Hotkey, INSTANCE_UUID, bodyHash}
		joinedMessage := strings.Join(message, ".")
		signedMessage := signMessage(joinedMessage, PUBLIC_KEY, PRIVATE_KEY)
		port := fmt.Sprint(firstMiner.Port)
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
				Ip:            firstMiner.Ip,
				Port:          &port,
				Hotkey:        firstMiner.Hotkey,
			},
			SamplingParams: SamplingParams{
				Seed:                nil,
				Truncate:            nil,
				BestOf:              1,
				DecoderInputDetails: true,
				Details:             false,
				DoSample:            true,
				MaxNewTokens:        1024,
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

		endpoint := "http://" + firstMiner.Ip + ":" + fmt.Sprint(firstMiner.Port) + "/Inference"
		out, err := json.Marshal(body)
		r, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(out))
		if err != nil {
			panic(err)
		}

		r.Header["Content-Type"] = []string{"application/json"}
		r.Header["name"] = []string{"Inference"}
		r.Header["timeout"] = []string{"12.0"}
		r.Header["bt_header_axon_ip"] = []string{firstMiner.Ip}
		r.Header["bt_header_axon_port"] = []string{strconv.Itoa(firstMiner.Port)}
		r.Header["bt_header_axon_hotkey"] = []string{firstMiner.Hotkey}
		r.Header["bt_header_dendrite_ip"] = []string{IP}
		r.Header["bt_header_dendrite_version"] = []string{"670"}
		r.Header["bt_header_dendrite_nonce"] = []string{strconv.Itoa(int(nonce))}
		r.Header["bt_header_dendrite_uuid"] = []string{INSTANCE_UUID}
		r.Header["bt_header_dendrite_hotkey"] = []string{HOTKEY}
		r.Header["bt_header_input_obj_sources"] = []string{"W10="}
		r.Header["bt_header_input_obj_query"] = []string{"IiI="}
		r.Header["bt_header_dendrite_signature"] = []string{signedMessage}
		r.Header["header_size"] = []string{"111"}
		r.Header["total_size"] = []string{"111"}
		r.Header["computed_body_hash"] = []string{bodyHash}
		client := &http.Client{}
		res, err := client.Do(r)
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.Body)
			panic(string(body))
		}
		c.Response().Header().Set("Access-Control-Allow-Origin", "*")
		c.Response().Header().Set("Access-Control-Expose-Headers", "Content-Type")

		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().WriteHeader(http.StatusOK)
		reader := bufio.NewReader(res.Body)
		//p := make([]byte, 4)
		for {
			tokens, err := reader.ReadString(' ')
			if err == io.EOF {
				break
			}
			fmt.Fprintf(c.Response(), tokens)
			c.Response().Flush()
		}
		return c.String(200, "")
	})
	e.GET("/", func(c echo.Context) error {
		c.Response().Header().Set("Access-Control-Allow-Origin", "*")
		c.Response().Header().Set("Access-Control-Expose-Headers", "Content-Type")

		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		for i := 0; i < 10; i++ {
			fmt.Fprintf(c.Response(), "data: %s\n\n", fmt.Sprintf("Event %d", i))
			time.Sleep(2 * time.Second)
			c.Response().Flush()
		}
		return c.NoContent(http.StatusOK)
	})
	e.Logger.Fatal(e.Start(":80"))
}
