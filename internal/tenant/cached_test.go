package tenant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

type stubResolver struct {
	calls int
	t     *Tenant
	err   error
}

func (s *stubResolver) GetByAPIKey(context.Context, string) (*Tenant, error) {
	s.calls++
	return s.t, s.err
}

func TestCachedRepository_CachesHitsByHash(t *testing.T) {
	mr := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	inner := &stubResolver{t: &Tenant{ID: "t1", Status: "active"}}
	c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}

	for range 3 {
		got, err := c.GetByAPIKey(t.Context(), "raw-key")
		if err != nil || got.ID != "t1" {
			t.Fatalf("got %+v, %v", got, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
	for _, k := range mr.Keys() {
		if k != "apikey:"+HashAPIKey("raw-key") {
			t.Fatalf("unexpected key %q (raw key must never be stored)", k)
		}
	}
}

func TestCachedRepository_DoesNotCacheMisses(t *testing.T) {
	rds := goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()})
	inner := &stubResolver{err: ErrNotFound}
	c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}

	for range 2 {
		if _, err := c.GetByAPIKey(t.Context(), "bad"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	}
	if inner.calls != 2 {
		t.Fatalf("inner calls = %d, want 2", inner.calls)
	}
}
