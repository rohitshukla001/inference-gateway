// Package provider defines the abstraction for LLM inference providers.
package provider

import (
	"context"
	"errors"
	"net"
)

// InferenceRequest is a provider-agnostic inference request.
type InferenceRequest struct {
	Prompt string
	Model  string
	// MaxTokens caps the completion length; 0 means the provider default.
	MaxTokens int32
}

// InferenceResponse is a provider-agnostic inference response.
type InferenceResponse struct {
	Output           string
	Model            string
	Provider         string
	PromptTokens     int32
	CompletionTokens int32
	TotalTokens      int32
}

// Pricing holds token rates in USD per 1k tokens. Local providers report zero.
type Pricing struct {
	InputPer1k  float64
	OutputPer1k float64
}

// Cost returns the USD cost of the given token counts.
func (p Pricing) Cost(inputTokens, outputTokens int32) float64 {
	return (float64(inputTokens)/1000.0)*p.InputPer1k +
		(float64(outputTokens)/1000.0)*p.OutputPer1k
}

// Provider is the interface every LLM backend implements.
type Provider interface {
	// Name returns the provider identifier, e.g. "ollama" or "mock".
	Name() string

	Execute(ctx context.Context, req *InferenceRequest) (*InferenceResponse, error)

	// HealthCheck returns nil if the provider is reachable.
	HealthCheck(ctx context.Context) error

	// Pricing returns the rates for model, or ok=false if it is unknown.
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

// IsRetryable reports whether retrying the same provider could succeed.
// NonRetryable errors are not, nor are timeouts and cancellations: a call
// that used its whole time budget would only use it again.
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
