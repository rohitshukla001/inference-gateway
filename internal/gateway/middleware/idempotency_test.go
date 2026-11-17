package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/rshuklas/inference-gateway/internal/tenant"
)

// The passthrough tests need no Redis; the rest run against miniredis.

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

func idemRequest(key, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set(IdempotencyHeader, key)
	return r.WithContext(tenant.WithTenant(r.Context(), &tenant.Tenant{ID: "t1"}))
}

func newIdemRedis(t *testing.T) *goredis.Client {
	t.Helper()
	return goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()})
}

func TestIdempotency_ConcurrentDuplicateIsRejectedWhileInFlight(t *testing.T) {
	rds := newIdemRedis(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	var calls atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"output":"first"}`))
	})
	h := Idempotency(rds, time.Hour)(next)

	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(first, idemRequest("k1", `{"prompt":"x"}`)); close(done) }()
	<-entered

	dup := httptest.NewRecorder()
	h.ServeHTTP(dup, idemRequest("k1", `{"prompt":"x"}`))
	if dup.Code != http.StatusConflict {
		t.Fatalf("in-flight duplicate: got %d, want 409", dup.Code)
	}

	close(release)
	<-done

	replayed := httptest.NewRecorder()
	h.ServeHTTP(replayed, idemRequest("k1", `{"prompt":"x"}`))
	if replayed.Code != http.StatusOK || replayed.Header().Get("Idempotent-Replay") != "true" || replayed.Body.String() != `{"output":"first"}` {
		t.Fatalf("replay: code=%d hdr=%q body=%q", replayed.Code, replayed.Header().Get("Idempotent-Replay"), replayed.Body.String())
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("handler ran %d times, want 1", n)
	}
}

func TestIdempotency_DifferentBodyIsRejected(t *testing.T) {
	rds := newIdemRedis(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := Idempotency(rds, time.Hour)(next)

	h.ServeHTTP(httptest.NewRecorder(), idemRequest("k1", `{"prompt":"a"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, idemRequest("k1", `{"prompt":"b"}`))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422", rr.Code)
	}
}

func TestIdempotency_FailureReleasesKey(t *testing.T) {
	rds := newIdemRedis(t)
	var calls atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := Idempotency(rds, time.Hour)(next)

	h.ServeHTTP(httptest.NewRecorder(), idemRequest("k1", `{}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, idemRequest("k1", `{}`))
	if rr.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("retry after failure: code=%d calls=%d, want 200 and 2", rr.Code, calls.Load())
	}
}

func TestIdempotency_HandlerSeesOriginalBody(t *testing.T) {
	rds := newIdemRedis(t)
	var got string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusOK)
	})
	Idempotency(rds, time.Hour)(next).ServeHTTP(httptest.NewRecorder(), idemRequest("k1", `{"prompt":"x"}`))
	if got != `{"prompt":"x"}` {
		t.Fatalf("handler body = %q", got)
	}
}
