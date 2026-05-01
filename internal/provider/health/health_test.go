package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

type fakeProvider struct {
	name    string
	failing atomic.Bool
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Execute(_ context.Context, _ *provider.InferenceRequest) (*provider.InferenceResponse, error) {
	return nil, errors.New("not used")
}

func (f *fakeProvider) HealthCheck(_ context.Context) error {
	if f.failing.Load() {
		return errors.New("simulated unhealthy")
	}
	return nil
}

func (f *fakeProvider) Pricing(_ string) (provider.Pricing, bool) { return provider.Pricing{}, true }

func TestNew_OptimisticInitialState(t *testing.T) {
	p := &fakeProvider{name: "p"}
	c := New(map[string]provider.Provider{"p": p}, 0, time.Second)

	if !c.IsHealthy("p") {
		t.Error("expected initial state to be healthy")
	}
}

func TestIsHealthy_UnknownProviderReturnsFalse(t *testing.T) {
	c := New(map[string]provider.Provider{}, 0, time.Second)
	if c.IsHealthy("missing") {
		t.Error("expected unknown provider to be unhealthy")
	}
}

func TestUpdate_TransitionsState(t *testing.T) {
	p := &fakeProvider{name: "p"}
	c := New(map[string]provider.Provider{"p": p}, 0, time.Second)

	p.failing.Store(true)
	c.checkAll(context.Background())
	if c.IsHealthy("p") {
		t.Error("expected unhealthy after failed check")
	}

	p.failing.Store(false)
	c.checkAll(context.Background())
	if !c.IsHealthy("p") {
		t.Error("expected healthy after successful check")
	}
}

func TestSnapshot_ReturnsCopy(t *testing.T) {
	p := &fakeProvider{name: "p"}
	c := New(map[string]provider.Provider{"p": p}, 0, time.Second)
	c.checkAll(context.Background())

	snap := c.Snapshot()
	snap["p"] = State{Healthy: false}

	if !c.IsHealthy("p") {
		t.Error("mutating the snapshot should not affect the checker")
	}
}

func TestStart_PingsOnInterval(t *testing.T) {
	p := &fakeProvider{name: "p"}
	p.failing.Store(true)
	c := New(map[string]provider.Provider{"p": p}, 20*time.Millisecond, 50*time.Millisecond)

	c.Start(t.Context())

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !c.IsHealthy("p") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("expected provider to be marked unhealthy by background pinger")
}

func TestHandler_ReturnsJSONSnapshot(t *testing.T) {
	p := &fakeProvider{name: "p"}
	c := New(map[string]provider.Provider{"p": p}, 0, time.Second)
	c.checkAll(context.Background())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/providers", nil)
	c.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(body.Providers) != 1 {
		t.Fatalf("expected 1 provider entry, got %d", len(body.Providers))
	}
	if body.Providers[0]["name"] != "p" {
		t.Errorf("expected name=p, got %v", body.Providers[0]["name"])
	}
	if body.Providers[0]["healthy"] != true {
		t.Errorf("expected healthy=true, got %v", body.Providers[0]["healthy"])
	}
}