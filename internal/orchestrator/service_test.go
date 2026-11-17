package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/provider"
	"github.com/rshuklas/inference-gateway/internal/router"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
)

// stubProvider is a minimal Provider implementation that the service can
// register under any name; it implements just enough surface for the
// orchestrator's flow tests.
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

// newTestService wires a Service against a single stub provider registered
// under whichever provider name the catalog will route to. The router is
// pinned to the default catalog so the routing path is exercised end-to-end.
func newTestService(t *testing.T, p *stubProvider, registerAs string) *Service {
	t.Helper()
	return NewService(Deps{
		Providers: map[string]provider.Provider{registerAs: p},
		Router:    router.New(router.DefaultCatalog()),
	})
}

func TestExecuteInference_RejectsEmptyPrompt(t *testing.T) {
	p := &stubProvider{name: "ollama"}
	svc := newTestService(t, p, "ollama")

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
	p := &stubProvider{name: "ollama"}
	svc := newTestService(t, p, "ollama")

	_, err := svc.ExecuteInference(context.Background(), &pb.ExecuteInferenceRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected Unauthenticated when no tenant in context")
	}
	if atomic.LoadInt64(&p.calls) != 0 {
		t.Errorf("expected 0 provider calls without tenant, got %d", p.calls)
	}
}

func TestExecuteInference_RoutesAndCallsProvider(t *testing.T) {
	p := &stubProvider{
		name: "ollama",
		resp: &provider.InferenceResponse{
			Output: "hello", Model: "llama3.2", Provider: "ollama",
			PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8,
		},
	}
	svc := newTestService(t, p, "ollama")

	ctx := tenant.WithTenant(context.Background(), &tenant.Tenant{ID: "tid-1"})
	// Explicit model so the router picks ollama deterministically.
	resp, err := svc.ExecuteInference(ctx, &pb.ExecuteInferenceRequest{
		Prompt: "hi",
		Model:  "llama3.2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.GetOutput() != "hello" {
		t.Errorf("expected output 'hello', got %q", resp.GetOutput())
	}
	if resp.GetCached() {
		t.Error("expected cached=false on a fresh call with no cache configured")
	}
	if atomic.LoadInt64(&p.calls) != 1 {
		t.Errorf("expected exactly 1 provider call, got %d", p.calls)
	}
}

func TestExecuteInference_FallbackTakesOverWhenPrimaryFails(t *testing.T) {
	primary := &stubProvider{name: "mock", err: errors.New("primary boom")}
	fallback := &stubProvider{
		name: "ollama",
		resp: &provider.InferenceResponse{
			Output: "fallback-output", Model: "llama3.2", Provider: "ollama",
			PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2,
		},
	}
	svc := NewService(Deps{
		Providers: map[string]provider.Provider{
			"mock":   primary,
			"ollama": fallback,
		},
		Router: router.New(router.DefaultCatalog()),
	})

	ctx := tenant.WithTenant(context.Background(), &tenant.Tenant{ID: "tid-1"})
	// Explicit gpt-4o-mini → primary is mock, fallback is ollama by catalog rules.
	resp, err := svc.ExecuteInference(ctx, &pb.ExecuteInferenceRequest{
		Prompt: "hi",
		Model:  "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetOutput() != "fallback-output" {
		t.Errorf("expected fallback output, got %q", resp.GetOutput())
	}
	if atomic.LoadInt64(&primary.calls) != 1 {
		t.Errorf("expected 1 primary call, got %d", primary.calls)
	}
	if atomic.LoadInt64(&fallback.calls) != 1 {
		t.Errorf("expected 1 fallback call, got %d", fallback.calls)
	}
}

type stubHealth struct {
	unhealthy map[string]bool
}

func (s *stubHealth) IsHealthy(name string) bool { return !s.unhealthy[name] }

func TestExecuteInference_SkipsKnownUnhealthyPrimary(t *testing.T) {
	primary := &stubProvider{name: "mock"}
	fallback := &stubProvider{
		name: "ollama",
		resp: &provider.InferenceResponse{
			Output: "from-fallback", Model: "llama3.2", Provider: "ollama",
			PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2,
		},
	}
	svc := NewService(Deps{
		Providers: map[string]provider.Provider{
			"mock":   primary,
			"ollama": fallback,
		},
		Router: router.New(router.DefaultCatalog()),
		Health: &stubHealth{unhealthy: map[string]bool{"mock": true}},
	})

	ctx := tenant.WithTenant(context.Background(), &tenant.Tenant{ID: "tid-1"})
	resp, err := svc.ExecuteInference(ctx, &pb.ExecuteInferenceRequest{
		Prompt: "hi",
		Model:  "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetOutput() != "from-fallback" {
		t.Errorf("expected fallback output, got %q", resp.GetOutput())
	}
	if got := atomic.LoadInt64(&primary.calls); got != 0 {
		t.Errorf("expected 0 primary calls when known unhealthy, got %d", got)
	}
	if got := atomic.LoadInt64(&fallback.calls); got != 1 {
		t.Errorf("expected 1 fallback call, got %d", got)
	}
}

func TestExecuteInference_BothFail_ReturnsInternal(t *testing.T) {
	primary := &stubProvider{name: "mock", err: errors.New("primary boom")}
	fallback := &stubProvider{name: "ollama", err: errors.New("fallback boom too")}
	svc := NewService(Deps{
		Providers: map[string]provider.Provider{
			"mock":   primary,
			"ollama": fallback,
		},
		Router: router.New(router.DefaultCatalog()),
	})

	ctx := tenant.WithTenant(context.Background(), &tenant.Tenant{ID: "tid-1"})
	_, err := svc.ExecuteInference(ctx, &pb.ExecuteInferenceRequest{
		Prompt: "hi",
		Model:  "gpt-4o-mini",
	})
	if err == nil {
		t.Fatal("expected error when both primary and fallback fail")
	}
}

func TestHealthCheck_UsesHealthCheckerWhenWired(t *testing.T) {
	p := &stubProvider{name: "ollama"}
	for name, unhealthy := range map[string]bool{"healthy": false, "unhealthy": true} {
		t.Run(name, func(t *testing.T) {
			svc := NewService(Deps{
				Providers: map[string]provider.Provider{"ollama": p},
				Router:    router.New(router.DefaultCatalog()),
				Health:    &stubHealth{unhealthy: map[string]bool{"ollama": unhealthy}},
			})
			resp, err := svc.HealthCheck(context.Background(), &pb.HealthCheckRequest{})
			if err != nil {
				t.Fatal(err)
			}
			want := "SERVING"
			if unhealthy {
				want = "NOT_SERVING"
			}
			if resp.GetStatus() != want {
				t.Errorf("status = %q, want %q", resp.GetStatus(), want)
			}
		})
	}
}
