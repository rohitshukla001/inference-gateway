// Package test contains end-to-end integration tests for the inference gateway.
package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

const (
	gatewayURL = "http://localhost:8080"
	devAPIKey  = "dev-key-tenant-acme"
)

func TestHealthEndpoint(t *testing.T) {
	resp, err := http.Get(gatewayURL + "/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var result map[string]any
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

	req, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/inference/execute", bytes.NewReader(jsonBody))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+devAPIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["output"] == nil || result["output"] == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("inference output: %v", result["output"])
}

func TestExecuteInference_Unauthorized(t *testing.T) {
	body := map[string]string{"prompt": "hello"}
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

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", resp.StatusCode)
	}
}
