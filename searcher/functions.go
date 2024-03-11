package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ChainSafe/go-schnorrkel"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
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

func querySerper(query string, endpoint string) map[string]any {
	SERPER_KEY := safeEnv("SERPER_KEY")
	body, _ := json.Marshal(map[string]string{
		"q": query,
	})
	r, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(body))
	if err != nil {
		panic(err)
	}
	defer r.Body.Close()
	r.Header["Content-Type"] = []string{"application/json"}
	r.Header["X-API-KEY"] = []string{SERPER_KEY}
	client := &http.Client{}
	res, err := client.Do(r)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		panic(res.StatusCode)
	}
	resp := map[string]any{}
	json.NewDecoder(res.Body).Decode(&resp)
	return resp
}

func formatListToPythonString(list []string) string {
	h := sha3.New256()
	sourcesForHash := "["
	fmt.Print("\n----\n")
	for i, element := range list {
		separator := "'"
		if strings.Contains(element, "'") {
			separator = "\""
		}
		if i != 0 {
			sourcesForHash += ", "
		}
		element = strconv.Quote(element)
		element = strings.TrimPrefix(element, "\"")
		element = strings.TrimSuffix(element, "\"")
		sourcesForHash += separator + element + separator
	}
	sourcesForHash += "]"
	h.Write([]byte(sourcesForHash))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}

func sendEvent(c echo.Context, data any) {
	eventId := uuid.New().String()
	fmt.Fprintf(c.Response(), "event: new_event\n")
	fmt.Fprintf(c.Response(), "id: %s\n", eventId)
	fmt.Fprintf(c.Response(), "retry: %d\n", 1500)
	eventData, _ := json.Marshal(data)
	fmt.Fprintf(c.Response(), "data: %s\n", string(eventData))
	c.Response().Flush()
}
