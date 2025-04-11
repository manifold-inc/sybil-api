package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

type ErrorReport struct {
	Service   string `json:"service"`
	Endpoint  string `json:"endpoint"`
	Error     string `json:"error"`
	Traceback string `json:"traceback,omitempty"`
}

func testEndon(ENDON_URL string) {
	// Create a test error
	err := fmt.Errorf("test integration error")

	// Send to Endon
	sendErrorToEndon(ENDON_URL, err, "integration_test")
}

func sendErrorToEndon(ENDON_URL string, err error, endpoint string) {
	payload := ErrorReport{
		Service:  "sybil",
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

func main() {
	res, present := os.LookupEnv("ENDON_URL")
	if !present {
		fmt.Printf("Missing environment variable %s\n", "ENDON_URL")
		return
	}

	testEndon(res)
}
