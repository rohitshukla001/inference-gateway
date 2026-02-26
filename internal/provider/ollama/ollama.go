// Package ollama implements the provider interface for the Ollama local LLM runtime.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

// ollamaGenerateRequest is the payload sent to Ollama's /api/generate endpoint.
type ollamaGenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// ollamaGenerateResponse is the response from Ollama's /api/generate endpoint.
type ollamaGenerateResponse struct {
	Model           string `json:"model"`
	Response        string `json:"response"`
	Done            bool   `json:"done"`
	TotalDuration   int64  `json:"total_duration"`
	PromptEvalCount int32  `json:"prompt_eval_count"`
	EvalCount       int32  `json:"eval_count"`
}

// OllamaProvider implements the Provider interface for Ollama.
type OllamaProvider struct {
	endpoint     string
	defaultModel string
	httpClient   *http.Client
}

// New creates a new OllamaProvider with the given endpoint and default model.
func New(endpoint, defaultModel string) *OllamaProvider {
	return &OllamaProvider{
		endpoint:     endpoint,
		defaultModel: defaultModel,
		httpClient: &http.Client{
			Timeout: 120 * time.Second, // LLM inference can be slow
		},
	}
}

// Name returns the provider identifier.
func (o *OllamaProvider) Name() string {
	return "ollama"
}

// Execute sends a prompt to Ollama and returns the generated response.
func (o *OllamaProvider) Execute(ctx context.Context, req *provider.InferenceRequest) (*provider.InferenceResponse, error) {
	model := req.Model
	if model == "" {
		model = o.defaultModel
	}

	ollamaReq := ollamaGenerateRequest{
		Model:  model,
		Prompt: req.Prompt,
		Stream: false, // Non-streaming for Phase 1
	}

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ollama request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var ollamaResp ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode ollama response: %w", err)
	}

	return &provider.InferenceResponse{
		Output:           ollamaResp.Response,
		Model:            ollamaResp.Model,
		Provider:         o.Name(),
		PromptTokens:     ollamaResp.PromptEvalCount,
		CompletionTokens: ollamaResp.EvalCount,
		TotalTokens:      ollamaResp.PromptEvalCount + ollamaResp.EvalCount,
	}, nil
}

// HealthCheck verifies that Ollama is reachable.
func (o *OllamaProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.endpoint+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned unhealthy status: %d", resp.StatusCode)
	}

	return nil
}
