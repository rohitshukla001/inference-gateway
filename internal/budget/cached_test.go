package budget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

type stubReader struct {
	limitsCalls int
	mtdCalls    int
	limits      *Limits
	limitsErr   error
	reserve     *Reservation
}

func (s *stubReader) GetLimits(_ context.Context, _ string) (*Limits, error) {
	s.limitsCalls++
	return s.limits, s.limitsErr
}

func (s *stubReader) Reserve(_ context.Context, _ string, _, _ float64) (*Reservation, error) {
	s.mtdCalls++
	return s.reserve, nil
}

func (s *stubReader) Settle(_ context.Context, _ *Reservation, _ float64) error { return nil }

func newTestRedis(t *testing.T) *goredis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
}

func TestCached_GetLimits_HitSkipsInner(t *testing.T) {
	rds := newTestRedis(t)
	inner := &stubReader{limits: &Limits{TenantID: "t1", MonthlyLimitUSD: 10}}
	c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}

	if _, err := c.GetLimits(t.Context(), "t1"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if inner.limitsCalls != 1 {
		t.Fatalf("expected 1 inner call after miss, got %d", inner.limitsCalls)
	}

	got, err := c.GetLimits(t.Context(), "t1")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if inner.limitsCalls != 1 {
		t.Errorf("expected inner to be skipped on hit, got %d total calls", inner.limitsCalls)
	}
	if got.MonthlyLimitUSD != 10 {
		t.Errorf("unexpected limit: %+v", got)
	}
}

func TestCached_GetLimits_MissPropagatesInnerError(t *testing.T) {
	rds := newTestRedis(t)
	want := errors.New("db boom")
	inner := &stubReader{limitsErr: want}
	c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}

	_, err := c.GetLimits(t.Context(), "t1")
	if !errors.Is(err, want) {
		t.Errorf("expected inner error, got %v", err)
	}
}

func TestCached_Reserve_AlwaysHitsInner(t *testing.T) {
	rds := newTestRedis(t)
	inner := &stubReader{reserve: &Reservation{SpentUSD: 4.2}}
	c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}

	for range 3 {
		got, err := c.Reserve(t.Context(), "t1", 0.1, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.SpentUSD != 4.2 {
			t.Errorf("expected 4.2, got %f", got.SpentUSD)
		}
	}
	if inner.mtdCalls != 3 {
		t.Errorf("expected Reserve to bypass cache (3 calls), got %d", inner.mtdCalls)
	}
}

func TestCached_GetLimits_RedisFailureFallsThrough(t *testing.T) {
	rds := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	inner := &stubReader{limits: &Limits{TenantID: "t1", MonthlyLimitUSD: 5}}
	c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	got, err := c.GetLimits(ctx, "t1")
	if err != nil {
		t.Fatalf("expected fall-through, got %v", err)
	}
	if got.MonthlyLimitUSD != 5 {
		t.Errorf("expected inner result, got %+v", got)
	}
}
