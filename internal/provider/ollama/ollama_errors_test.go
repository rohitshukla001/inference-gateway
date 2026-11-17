package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

func TestExecute_ClassifiesStatusCodes(t *testing.T) {
	cases := map[int]bool{ // status -> retryable
		http.StatusNotFound:            false,
		http.StatusBadRequest:          false,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusServiceUnavailable:  true,
	}
	for code, wantRetryable := range cases {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
			defer srv.Close()
			_, err := New(srv.URL, "llama3.2", time.Second).Execute(context.Background(), &provider.InferenceRequest{Prompt: "hi"})
			if err == nil || provider.IsRetryable(err) != wantRetryable {
				t.Fatalf("err=%v retryable=%v, want %v", err, provider.IsRetryable(err), wantRetryable)
			}
		})
	}
}

func TestExecute_TimeoutIsNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(200 * time.Millisecond) }))
	defer srv.Close()
	_, err := New(srv.URL, "llama3.2", 20*time.Millisecond).Execute(context.Background(), &provider.InferenceRequest{Prompt: "hi"})
	if err == nil || provider.IsRetryable(err) {
		t.Fatalf("err=%v, want a non-retryable timeout", err)
	}
}

func TestExecute_SendsMaxTokens(t *testing.T) {
	var got ollamaGenerateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"model":"llama3.2","response":"ok","done":true}`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "llama3.2", time.Second).Execute(context.Background(), &provider.InferenceRequest{Prompt: "hi", MaxTokens: 512}); err != nil {
		t.Fatal(err)
	}
	if got.Options == nil || got.Options.NumPredict != 512 {
		t.Fatalf("options = %+v, want num_predict 512", got.Options)
	}
}
