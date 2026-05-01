package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/provider"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
)

// Most coverage of cache + usage lives in the live e2e flow because the
// real types are tightly coupled to redis + pgx. Here we verify the contract
// the service holds with the provider: tenant required, prompt required,
// provider invoked exactly once when cache is absent.

type stubProvider struct {
	name  string
	calls int64
	resp  *provider.InferenceResponse
	err   error
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Execute(_ context.Context, _ *provider.InferenceRequest) (*provider.InferenceResponse, error) {
	atomic.AddInt64(&s.calls, 1)
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func (s *stubProvider) HealthCheck(_ context.Context) error { return nil }

func (s *stubProvider) Pricing(_ string) (provider.Pricing, bool) {
	return provider.Pricing{}, true
}

func TestExecuteInference_RejectsEmptyPrompt(t *testing.T) {
	p := &stubProvider{name: "stub"}
	svc := NewService(p, nil, nil)

	ctx := tenant.WithTenant(context.Background(), &tenant.Tenant{ID: "tid-1"})
	_, err := svc.ExecuteInference(ctx, &pb.ExecuteInferenceRequest{Prompt: ""})
	if err == nil {
		t.Fatal("expected InvalidArgument for empty prompt")
	}
	if atomic.LoadInt64(&p.calls) != 0 {
		t.Errorf("expected 0 provider calls on bad input, got %d", p.calls)
	}
}

func TestExecuteInference_RejectsMissingTenant(t *testing.T) {
	p := &stubProvider{name: "stub"}
	svc := NewService(p, nil, nil)

	_, err := svc.ExecuteInference(context.Background(), &pb.ExecuteInferenceRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected Unauthenticated when no tenant in context")
	}
	if atomic.LoadInt64(&p.calls) != 0 {
		t.Errorf("expected 0 provider calls without tenant, got %d", p.calls)
	}
}

func TestExecuteInference_NoCache_CallsProvider(t *testing.T) {
	p := &stubProvider{
		name: "stub",
		resp: &provider.InferenceResponse{
			Output: "hello", Model: "m1", Provider: "stub",
			PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8,
		},
	}
	svc := NewService(p, nil, nil)

	ctx := tenant.WithTenant(context.Background(), &tenant.Tenant{ID: "tid-1"})
	resp, err := svc.ExecuteInference(ctx, &pb.ExecuteInferenceRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.GetOutput() != "hello" {
		t.Errorf("expected output 'hello', got %q", resp.GetOutput())
	}
	if resp.GetCached() {
		t.Error("expected cached=false when no cache configured")
	}
	if atomic.LoadInt64(&p.calls) != 1 {
		t.Errorf("expected exactly 1 provider call, got %d", p.calls)
	}
}

func TestExecuteInference_ProviderError_PropagatesInternal(t *testing.T) {
	p := &stubProvider{name: "stub", err: errors.New("boom")}
	svc := NewService(p, nil, nil)

	ctx := tenant.WithTenant(context.Background(), &tenant.Tenant{ID: "tid-1"})
	_, err := svc.ExecuteInference(ctx, &pb.ExecuteInferenceRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected error when provider fails")
	}
}
