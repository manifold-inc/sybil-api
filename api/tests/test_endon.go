package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

func testEndon(ENDON_URL string) {
	// Create a test error
	err := fmt.Errorf("test integration error")

	// Send to Endon
	sendErrorToEndon(ENDON_URL, err, "integration_test")
}

func sendErrorToEndon(ENDON_URL string, err error, endpoint string) {

	payload := map[string]interface{}{
		"error":     err.Error(),
		"endpoint":  endpoint,
		"timestamp": float64(time.Now().UnixNano()) / 1e9,
	}

	jsonData, jsonErr := json.Marshal(payload)
	if jsonErr != nil {
		fmt.Printf("Failed to marshal error payload: %v", jsonErr)
		return
	}

	client := &http.Client{}

	req, reqErr := http.NewRequest(http.MethodPost, ENDON_URL, bytes.NewBuffer(jsonData))
	if reqErr != nil {
		fmt.Printf("Failed to create Endon request: %v", reqErr)
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, respErr := client.Do(req)
	if respErr != nil {
		fmt.Printf("Failed to send error to Endon: %v", respErr)
		return
	}

	fmt.Printf("Successfully sent error to Endon")
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		fmt.Printf("Failed to report error to Endon. Status: %d", resp.StatusCode)
	}
}

func main() {
	res, present := os.LookupEnv("ENDON_URL")
	if !present {
		fmt.Printf("Missing environment variable %s\n", "ENDON_URL")
		return
	}

	testEndon(res)
}
