package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// These tests cover the no-Redis passthrough paths. End-to-end replay is
// verified live against the actual Redis container.

func TestIdempotency_NoHeader_Passthrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Passing nil client is fine because the middleware short-circuits before
	// touching Redis when no header is present.
	rr := httptest.NewRecorder()
	Idempotency(nil, time.Hour)(next).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", nil))

	if !called {
		t.Error("expected next handler to be called when no Idempotency-Key header is sent")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestIdempotency_NoTenant_Passthrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(IdempotencyHeader, "any-key")

	rr := httptest.NewRecorder()
	Idempotency(nil, time.Hour)(next).ServeHTTP(rr, r)

	if !called {
		t.Error("expected next handler to be called when no tenant in ctx")
	}
}
