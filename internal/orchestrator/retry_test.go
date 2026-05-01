package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

func TestExecuteWithRetry_SucceedsFirstAttempt(t *testing.T) {
	p := &stubProvider{
		name: "ollama",
		resp: &provider.InferenceResponse{Output: "ok"},
	}
	svc := &Service{retry: RetryConfig{MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond}}

	resp, err := svc.executeWithRetry(context.Background(), p, &provider.InferenceRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output != "ok" {
		t.Errorf("expected ok, got %q", resp.Output)
	}
	if got := atomic.LoadInt64(&p.calls); got != 1 {
		t.Errorf("expected 1 call, got %d", got)
	}
}

func TestExecuteWithRetry_RecoversAfterTransientFailure(t *testing.T) {
	p := &flakyProvider{failuresBeforeSuccess: 2, resp: &provider.InferenceResponse{Output: "recovered"}}
	svc := &Service{retry: RetryConfig{MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}}

	resp, err := svc.executeWithRetry(context.Background(), p, &provider.InferenceRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output != "recovered" {
		t.Errorf("expected recovered, got %q", resp.Output)
	}
	if got := atomic.LoadInt64(&p.calls); got != 3 {
		t.Errorf("expected 3 calls, got %d", got)
	}
}

func TestExecuteWithRetry_ExhaustsAndReturnsLastErr(t *testing.T) {
	p := &stubProvider{name: "ollama", err: errors.New("boom")}
	svc := &Service{retry: RetryConfig{MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}}

	_, err := svc.executeWithRetry(context.Background(), p, &provider.InferenceRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected wrapped lastErr, got %v", err)
	}
	if got := atomic.LoadInt64(&p.calls); got != 3 {
		t.Errorf("expected 3 calls, got %d", got)
	}
}

func TestExecuteWithRetry_ZeroAttemptsBecomesOne(t *testing.T) {
	p := &stubProvider{name: "ollama", err: errors.New("boom")}
	svc := &Service{retry: RetryConfig{}}

	_, err := svc.executeWithRetry(context.Background(), p, &provider.InferenceRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt64(&p.calls); got != 1 {
		t.Errorf("expected exactly 1 call when MaxAttempts=0, got %d", got)
	}
}

func TestExecuteWithRetry_StopsWhenContextCancelled(t *testing.T) {
	p := &stubProvider{name: "ollama", err: errors.New("boom")}
	svc := &Service{retry: RetryConfig{MaxAttempts: 5, BaseBackoff: 50 * time.Millisecond, MaxBackoff: 200 * time.Millisecond}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.executeWithRetry(ctx, p, &provider.InferenceRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if got := atomic.LoadInt64(&p.calls); got > 1 {
		t.Errorf("expected at most 1 call before context cancel was noticed, got %d", got)
	}
}

func TestBackoffDelay_CapsAtMaxBackoff(t *testing.T) {
	base := 10 * time.Millisecond
	maxB := 50 * time.Millisecond
	for range 100 {
		d := backoffDelay(10, base, maxB)
		if d < 0 || d >= maxB {
			t.Fatalf("delay %s out of [0, %s)", d, maxB)
		}
	}
}

func TestBackoffDelay_ZeroBaseReturnsZero(t *testing.T) {
	if d := backoffDelay(0, 0, time.Second); d != 0 {
		t.Errorf("expected 0 with zero base, got %s", d)
	}
}

// flakyProvider succeeds on the (failuresBeforeSuccess+1)th call.
type flakyProvider struct {
	calls                 int64
	failuresBeforeSuccess int64
	resp                  *provider.InferenceResponse
}

func (f *flakyProvider) Name() string { return "flaky" }

func (f *flakyProvider) Execute(_ context.Context, _ *provider.InferenceRequest) (*provider.InferenceResponse, error) {
	n := atomic.AddInt64(&f.calls, 1)
	if n <= f.failuresBeforeSuccess {
		return nil, errors.New("transient")
	}
	return f.resp, nil
}

func (f *flakyProvider) HealthCheck(_ context.Context) error              { return nil }
func (f *flakyProvider) Pricing(_ string) (provider.Pricing, bool)        { return provider.Pricing{}, true }