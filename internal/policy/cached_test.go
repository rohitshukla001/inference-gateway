package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

type stubReader struct {
	calls int
	pol   *Policy
	err   error
}

func (s *stubReader) Get(_ context.Context, _ string) (*Policy, error) {
	s.calls++
	return s.pol, s.err
}

func newTestRedis(t *testing.T) *goredis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
}

func TestCached_HitSkipsInner(t *testing.T) {
	rds := newTestRedis(t)
	inner := &stubReader{pol: &Policy{TenantID: "t1", DefaultTier: "default"}}
	c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}

	if _, err := c.Get(t.Context(), "t1"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("expected 1 inner call after miss, got %d", inner.calls)
	}

	got, err := c.Get(t.Context(), "t1")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if inner.calls != 1 {
		t.Errorf("expected inner to be skipped on hit, got %d total calls", inner.calls)
	}
	if got.TenantID != "t1" {
		t.Errorf("unexpected policy: %+v", got)
	}
}

func TestCached_MissPropagatesInnerError(t *testing.T) {
	rds := newTestRedis(t)
	want := errors.New("db boom")
	inner := &stubReader{err: want}
	c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}

	_, err := c.Get(t.Context(), "t1")
	if !errors.Is(err, want) {
		t.Errorf("expected inner error, got %v", err)
	}
}

func TestCached_RedisFailureFallsThrough(t *testing.T) {
	rds := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	inner := &stubReader{pol: &Policy{TenantID: "t1"}}
	c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	got, err := c.Get(ctx, "t1")
	if err != nil {
		t.Fatalf("expected fall-through to inner, got %v", err)
	}
	if got.TenantID != "t1" {
		t.Errorf("expected inner policy, got %+v", got)
	}
}
