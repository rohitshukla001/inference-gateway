// Package mock implements the Provider interface with simulated OpenAI-style
// models. It is used during development to exercise routing, fallback, and
// budget logic without burning real provider tokens.
package mock

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

// ModelSpec is the simulated cost + latency profile for a single mock model.
// Setting Fail=true causes Execute to error every time, useful for exercising
// the fallback path in tests.
type ModelSpec struct {
	Pricing   provider.Pricing
	LatencyMs int
	Fail      bool
}

// Provider implements provider.Provider with an in-memory model table.
type Provider struct {
	models map[string]ModelSpec
}

// DefaultModels returns the standard mock catalog mirroring real OpenAI rates
// (Aug 2024) so cost math during demos is realistic.
func DefaultModels() map[string]ModelSpec {
	return map[string]ModelSpec{
		"gpt-4o-mini": {
			Pricing:   provider.Pricing{InputPer1k: 0.00015, OutputPer1k: 0.0006},
			LatencyMs: 80,
		},
		"gpt-4o": {
			Pricing:   provider.Pricing{InputPer1k: 0.0025, OutputPer1k: 0.01},
			LatencyMs: 180,
		},
	}
}

// New returns a Provider seeded with DefaultModels.
func New() *Provider {
	return &Provider{models: DefaultModels()}
}

// NewWithModels returns a Provider seeded with custom specs. Used by tests
// that need failure injection or different rates.
func NewWithModels(specs map[string]ModelSpec) *Provider {
	return &Provider{models: specs}
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "mock" }

// Pricing returns the rates for model, or (zero, false) if model is unknown.
func (p *Provider) Pricing(model string) (provider.Pricing, bool) {
	spec, ok := p.models[model]
	if !ok {
		return provider.Pricing{}, false
	}
	return spec.Pricing, true
}

// Execute simulates a model call with the configured latency, returning a
// deterministic echo response for predictable demos.
func (p *Provider) Execute(ctx context.Context, req *provider.InferenceRequest) (*provider.InferenceResponse, error) {
	spec, ok := p.models[req.Model]
	if !ok {
		return nil, fmt.Errorf("mock: unknown model %q", req.Model)
	}
	if spec.Fail {
		return nil, errors.New("mock: simulated failure")
	}

	if spec.LatencyMs > 0 {
		select {
		case <-time.After(time.Duration(spec.LatencyMs) * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	output := fmt.Sprintf("[mock:%s] echo: %s", req.Model, req.Prompt)
	promptTokens := int32(len(strings.Fields(req.Prompt)))
	completionTokens := int32(len(strings.Fields(output)))

	return &provider.InferenceResponse{
		Output:           output,
		Model:            req.Model,
		Provider:         p.Name(),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}, nil
}

// HealthCheck is always healthy; the mock has no upstream to depend on.
func (p *Provider) HealthCheck(_ context.Context) error { return nil }
