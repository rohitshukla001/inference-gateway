// Package test contains end-to-end integration tests for the inference gateway.
package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

const gatewayURL = "http://localhost:8080"

func TestHealthEndpoint(t *testing.T) {
	resp, err := http.Get(gatewayURL + "/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["status"] != "SERVING" {
		t.Errorf("expected status SERVING, got %v", result["status"])
	}
}

func TestExecuteInference(t *testing.T) {
	body := map[string]string{
		"prompt": "Say hello in one word.",
	}
	jsonBody, _ := json.Marshal(body)

	resp, err := http.Post(
		gatewayURL+"/v1/inference/execute",
		"application/json",
		bytes.NewReader(jsonBody),
	)
	if err != nil {
		t.Fatalf("execute request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["output"] == nil || result["output"] == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("inference output: %v", result["output"])
}
