// Package provider defines the abstraction for LLM inference providers.
package provider

import "context"

// InferenceRequest represents a normalized request to any LLM provider.
type InferenceRequest struct {
	Prompt string
	Model  string
}

// InferenceResponse represents a normalized response from any LLM provider.
type InferenceResponse struct {
	Output           string
	Model            string
	Provider         string
	PromptTokens     int32
	CompletionTokens int32
	TotalTokens      int32
}

// Provider is the interface that all LLM providers must implement.
// This is the core abstraction that enables multi-provider support in later phases.
type Provider interface {
	// Name returns the provider identifier (e.g., "ollama", "openai").
	Name() string

	// Execute sends a prompt to the provider and returns the generated response.
	Execute(ctx context.Context, req *InferenceRequest) (*InferenceResponse, error)

	// HealthCheck verifies the provider is reachable and operational.
	HealthCheck(ctx context.Context) error
}
