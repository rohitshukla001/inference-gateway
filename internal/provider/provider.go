// Package provider defines the abstraction for LLM inference providers.
package provider

import (
	"context"
	"errors"
	"net"
)

// InferenceRequest represents a normalized request to any LLM provider.
type InferenceRequest struct {
	Prompt string
	Model  string
	// MaxTokens caps the completion length; 0 means the provider default.
	MaxTokens int32
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

// Pricing describes per-token cost rates for a provider in USD per 1k tokens.
// Local providers (Ollama) report zero rates.
type Pricing struct {
	InputPer1k  float64
	OutputPer1k float64
}

// Cost computes the post-flight cost for the given token counts.
func (p Pricing) Cost(inputTokens, outputTokens int32) float64 {
	return (float64(inputTokens)/1000.0)*p.InputPer1k +
		(float64(outputTokens)/1000.0)*p.OutputPer1k
}

// Provider is the interface that all LLM providers must implement.
// This is the core abstraction that enables multi-provider support.
type Provider interface {
	// Name returns the provider identifier (e.g., "ollama", "openai", "mock").
	Name() string

	// Execute sends a prompt to the provider and returns the generated response.
	Execute(ctx context.Context, req *InferenceRequest) (*InferenceResponse, error)

	// HealthCheck verifies the provider is reachable and operational.
	HealthCheck(ctx context.Context) error

	// Pricing returns the per-token rates for the given model. Returns zero
	// rates and ok=false for an unknown model.
	Pricing(model string) (Pricing, bool)
}

// nonRetryableError marks a failure that retrying cannot fix, such as a
// bad request or an unknown model.
type nonRetryableError struct{ err error }

func (e nonRetryableError) Error() string { return e.err.Error() }
func (e nonRetryableError) Unwrap() error { return e.err }

// NonRetryable wraps err so IsRetryable reports false for it.
func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryableError{err: err}
}

// IsRetryable reports whether another attempt at the same provider could
// succeed. Errors marked NonRetryable are not; neither are timeouts, since a
// call that already used its whole time budget would only use it again.
func IsRetryable(err error) bool {
	var nr nonRetryableError
	if errors.As(err, &nr) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return false
	}
	return true
}
