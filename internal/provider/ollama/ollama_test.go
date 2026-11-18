package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

func TestExecute_RespectsConfiguredTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := New(server.URL, "llama3.2", 50*time.Millisecond)

	start := time.Now()
	_, err := p.Execute(context.Background(), &provider.InferenceRequest{
		Prompt: "hello",
		Model:  "llama3.2",
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 150*time.Millisecond {
		t.Errorf("expected timeout to fire near 50ms, took %s", elapsed)
	}
}

func TestHealthCheck_RespectsConfiguredTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := New(server.URL, "llama3.2", 50*time.Millisecond)

	start := time.Now()
	err := p.HealthCheck(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 150*time.Millisecond {
		t.Errorf("expected timeout to fire near 50ms, took %s", elapsed)
	}
}
