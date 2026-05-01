package mock

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

func TestExecute_DefaultModel_Succeeds(t *testing.T) {
	p := New()
	resp, err := p.Execute(context.Background(), &provider.InferenceRequest{
		Prompt: "hello world",
		Model:  "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.Output, "hello world") {
		t.Errorf("expected output to echo prompt, got %q", resp.Output)
	}
	if resp.Provider != "mock" {
		t.Errorf("expected provider mock, got %s", resp.Provider)
	}
	if resp.PromptTokens == 0 || resp.CompletionTokens == 0 {
		t.Error("expected non-zero token counts")
	}
}

func TestExecute_UnknownModel_Errors(t *testing.T) {
	p := New()
	_, err := p.Execute(context.Background(), &provider.InferenceRequest{
		Prompt: "hi",
		Model:  "nonexistent-model",
	})
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestExecute_FailFlag_Errors(t *testing.T) {
	p := NewWithModels(map[string]ModelSpec{
		"broken": {Fail: true},
	})
	_, err := p.Execute(context.Background(), &provider.InferenceRequest{
		Prompt: "hi",
		Model:  "broken",
	})
	if err == nil {
		t.Fatal("expected error when Fail=true")
	}
}

func TestExecute_RespectsContextCancel(t *testing.T) {
	// Configure latency longer than the context allows so cancellation must win.
	p := NewWithModels(map[string]ModelSpec{
		"slow": {LatencyMs: 500},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := p.Execute(ctx, &provider.InferenceRequest{Prompt: "hi", Model: "slow"})
	if err == nil {
		t.Fatal("expected context error when latency exceeds deadline")
	}
}

func TestPricing_KnownModel(t *testing.T) {
	p := New()
	pricing, ok := p.Pricing("gpt-4o-mini")
	if !ok {
		t.Fatal("expected ok=true for known model")
	}
	if pricing.InputPer1k <= 0 || pricing.OutputPer1k <= 0 {
		t.Errorf("expected positive rates, got %+v", pricing)
	}
}

func TestPricing_UnknownModel(t *testing.T) {
	p := New()
	_, ok := p.Pricing("nope")
	if ok {
		t.Error("expected ok=false for unknown model")
	}
}
