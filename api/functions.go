package main

import (
	"bufio"
	"bytes"
	"math/rand"
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
	"time"

	"github.com/ChainSafe/go-schnorrkel"
	"github.com/aidarkhanov/nanoid"
	"github.com/google/uuid"
	"github.com/nitishm/go-rejson/v4"
	"golang.org/x/crypto/sha3"
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

func signMessage(message string, public string, private string) string {
	// Signs a message via schnorrkel pub and private keys

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

func querySearx(c *Context, query string, categories string, page int) (*SearxResponseBody, error) {
	res, err := http.PostForm(SEARX_URL+"/search", url.Values{
		"q":          {query},
		"format":     {"json"},
		"pageno":       {fmt.Sprint(page)},
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

func sha256Hash(str string) string {
	// hash a string via sha256

	h := sha3.New256()
	h.Write([]byte(str))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}

func formatListToPythonString(list []string) string {
	// Take a go list of strings and convert it to a pythonic version of the
	// string representaton of a list.

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

func getTopMiners(c *Context) []Miner {
	rh := rejson.NewReJSONHandler()
	rh.SetGoRedisClientWithContext(c.Request().Context(), client)
	minerJSON, err := rh.JSONGet("miners", ".")
	if err != nil {
		c.Err.Printf("Failed to JSONGet: %s\n", err.Error())
		return nil
	}

	var miners []Miner
	err = json.Unmarshal(minerJSON.([]byte), &miners)
	if err != nil {
		c.Err.Printf("Failed to JSON Unmarshal: %s\n", err.Error())
		return nil
	}
	for i := range miners {
			j := rand.Intn(i + 1)
			miners[i], miners[j] = miners[j], miners[i]
	}
	return miners
}

func queryMiners(c *Context, sources []string, query string) string {
	if DEBUG {
		return "No Answer"
	}
	// First we get our miners
	miners := getTopMiners(c)
	if miners == nil {
		return "No"
	}

	var hashes []string
	formatted := formatListToPythonString(sources)
	hashes = append(hashes, sha256Hash(formatted))
	hashes = append(hashes, sha256Hash(query))
	bodyHash := sha256Hash(strings.Join(hashes, ""))

	tr := &http.Transport{
		MaxIdleConns:      10,
		IdleConnTimeout:   30 * time.Second,
		DisableKeepAlives: false,
	}

	httpClient := http.Client{Transport: tr}

	nonce := time.Now().UnixNano()

	ctx := c.Request().Context()

	for index, miner := range miners {
		message := []string{fmt.Sprint(nonce), HOTKEY, miner.Hotkey, INSTANCE_UUID, bodyHash}
		joinedMessage := strings.Join(message, ".")
		signedMessage := signMessage(joinedMessage, PUBLIC_KEY, PRIVATE_KEY)
		port := fmt.Sprint(miner.Port)
		version := 672
		body := InferenceBody{
			Name:           "Inference",
			Timeout:        12.0,
			TotalSize:      0,
			HeaderSize:     0,
			RequiredFields: []string{"sources", "query", "seed"},
			Sources:        sources,
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
				Stop:                []string{},
				Temperature:         .01,
				TopK:                10,
				TopNTokens:          5,
				TopP:                .98,
				TypicalP:            .98,
				Watermark:           false,
			},
			Completion: nil,
		}

		endpoint := "http://" + miner.Ip + ":" + fmt.Sprint(miner.Port) + "/Inference"
		out, err := json.Marshal(body)
		r, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(out))
		if err != nil {
			c.Warn.Printf("Failed miner request: %s\n", err.Error())
			continue
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
		r.Header.Add("Accept-Encoding", "identity")

		res, err := httpClient.Do(r)
		if err != nil {
			c.Warn.Printf("Miner: %s %s\nError: %s\n", miner.Hotkey, miner.Coldkey, err.Error())
			if res != nil {
				res.Body.Close()
			}
			continue
		}
		if res.StatusCode != http.StatusOK {
			bdy, _ := io.ReadAll(res.Body)
			res.Body.Close()
			c.Warn.Printf("Miner: %s %s\nError: %s\n", miner.Hotkey, miner.Coldkey, string(bdy))
			continue
		}

		axon_version := res.Header.Get("Bt_header_axon_version")
		ver, err := strconv.Atoi(axon_version)
		if err != nil || ver < 672 {
			res.Body.Close()
			c.Warn.Printf("Miner: %s %s\nError: Axon version too low\n", miner.Hotkey, miner.Coldkey)
			continue
		}

		c.Info.Printf("Attempt: %d Miner: %s %s\n", index, miner.Hotkey, miner.Coldkey)
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
				finished = true
				break
			}
		}
		res.Body.Close()
		if finished == false {
			continue
		}
		return ans
	}
	return ""
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
