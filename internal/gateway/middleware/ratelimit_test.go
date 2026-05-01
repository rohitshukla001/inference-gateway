package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rshuklas/inference-gateway/internal/ratelimit"
	"github.com/rshuklas/inference-gateway/internal/tenant"
)

type stubRepo struct {
	rps int
	err error
}

func (s *stubRepo) GetRPS(_ context.Context, _ string) (int, error) {
	return s.rps, s.err
}

type stubChecker struct {
	decision ratelimit.Decision
	err      error
	calls    int
}

func (s *stubChecker) Allow(_ context.Context, _ string, _ int) (ratelimit.Decision, error) {
	s.calls++
	return s.decision, s.err
}

func newTenantRequest() *http.Request {
	r := httptest.NewRequest("POST", "/v1/inference/execute", nil)
	return r.WithContext(tenant.WithTenant(r.Context(), &tenant.Tenant{ID: "t1"}))
}

func TestRateLimit_AllowedPassesThrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := RateLimit(&stubRepo{rps: 10}, &stubChecker{decision: ratelimit.Decision{Allowed: true}})

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newTenantRequest())

	if !called {
		t.Error("expected next handler to be called when allowed")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRateLimit_DeniedReturns429WithRetryAfter(t *testing.T) {
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler must not be called when denied")
	})
	mw := RateLimit(&stubRepo{rps: 10}, &stubChecker{
		decision: ratelimit.Decision{Allowed: false, RetryAfter: 750 * time.Millisecond},
	})

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newTenantRequest())

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestRateLimit_NoTenantSkips(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	checker := &stubChecker{}
	mw := RateLimit(&stubRepo{rps: 10}, checker)

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, httptest.NewRequest("POST", "/", nil))

	if !called {
		t.Error("expected handler to be called when no tenant on context")
	}
	if checker.calls != 0 {
		t.Errorf("expected limiter not to be consulted without tenant, got %d calls", checker.calls)
	}
}

func TestRateLimit_RepoErrorFailsOpen(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := RateLimit(&stubRepo{err: errors.New("db down")}, &stubChecker{})

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newTenantRequest())

	if !called {
		t.Error("expected handler to be called on repo error (fail open)")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRateLimit_NotFoundIsUnlimited(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := RateLimit(&stubRepo{err: ratelimit.ErrNotFound}, &stubChecker{})

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newTenantRequest())

	if !called {
		t.Error("expected handler to be called when no rate limit row")
	}
}

func TestRateLimit_LimiterErrorFailsOpen(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := RateLimit(&stubRepo{rps: 5}, &stubChecker{err: errors.New("redis down")})

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, newTenantRequest())

	if !called {
		t.Error("expected handler to be called on limiter error (fail open)")
	}
}
