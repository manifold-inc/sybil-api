package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	schnorrkel "github.com/ChainSafe/go-schnorrkel"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/nitishm/go-rejson/v4"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/sha3"
)

func safeEnv(env string) string {
	res, present := os.LookupEnv(env)
	if !present {

		panic(fmt.Sprintf("Missing environment variable %s", env))
	}
	return res
}

func signMessage(message string, public string, private string) string {
	var pubk [32]byte
	data, err := hex.DecodeString(public)
	if err != nil {
		panic(err)
	}
	copy(pubk[:], data)

	var prik [32]byte
	data, err = hex.DecodeString(private)
	if err != nil {
		panic(err)
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

// Student - student object
type Miner struct {
	Ip     string `json:"ip,omitempty"`
	Port   int    `json:"port,omitempty"`
	Hotkey string `json:"hotkey,omitempty"`
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
	Nonce         *int    `json:"nonce"`
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

func formatListToPythonString(list []string) string {
	h := sha3.New256()
	sourcesForHash := "["
	for i, element := range list {
		if i != 0 {
			sourcesForHash += ", "
		}
		sourcesForHash += fmt.Sprintf("'%s'", element)
	}
	sourcesForHash += "]"
	h.Write([]byte(sourcesForHash))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}

func main() {
	hotkey := safeEnv("HOTKEY")
	ip := safeEnv("EXTERNAL_IP")
	publicKey := safeEnv("PUBLIC_KEY")
	privateKey := safeEnv("PRIVATE_KEY")

	// UUID for this instance. Persists through lifetime of service
	uuid := uuid.New().String()

	e := echo.New()
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr:     "cache:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	e.GET("/miners", func(c echo.Context) error {
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

		query := "How do you make cheese?" // TODO

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
		nonce := 999999999999999 // untill bittensor uses a non-py impl of a monotonic time

		var hashes []string

		sources := []string{"https://google.com"} // TODO get sources
		formatted := formatListToPythonString(sources)
		hashes = append(hashes, formatted)

		h := sha3.New256()
		h.Write([]byte(fmt.Sprint(query)))
		hashes = append(hashes, hex.EncodeToString(h.Sum(nil)))

		h = sha3.New256()
		h.Write([]byte(strings.Join(hashes, "")))
		bodyHash := hex.EncodeToString(h.Sum(nil))

		message := []string{fmt.Sprint(nonce), hotkey, firstMiner.Hotkey, uuid, bodyHash}
		joinedMessage := strings.Join(message, ".")
		signedMessage := signMessage(joinedMessage, publicKey, privateKey)
		port := fmt.Sprint(firstMiner.Port)
		version := 670
		body := SearchBody{
			Name:           "Inference",
			Timeout:        12.0,
			TotalSize:      0,
			HeaderSize:     0,
			RequiredFields: []string{"sources", "query", "seed"},
			Sources:        sources,
			Query:          query,
			BodyHash:       "",
			Dendrite: DendriteOrAxon{
				Ip:            ip,
				Version:       &version,
				Nonce:         &nonce,
				Uuid:          &uuid,
				Hotkey:        hotkey,
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
				MaxNewTokens:        100,
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
		out, err := json.MarshalIndent(body, "", "\t")
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
		r.Header["bt_header_dendrite_ip"] = []string{ip}
		r.Header["bt_header_dendrite_version"] = []string{"670"}
		r.Header["bt_header_dendrite_nonce"] = []string{strconv.Itoa(int(nonce))}
		r.Header["bt_header_dendrite_uuid"] = []string{uuid}
		r.Header["bt_header_dendrite_hotkey"] = []string{hotkey}
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
		if res.StatusCode == http.StatusOK {
			bodyBytes, err := io.ReadAll(res.Body)
			if err != nil {
				log.Fatal(err)
			}
			bodyString := string(bodyBytes)
			fmt.Printf("Response from %s. Query: %s\n", endpoint, query)
			println(bodyString)
			println()
			return c.String(200, bodyString)
		}
		return c.NoContent(500)
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
