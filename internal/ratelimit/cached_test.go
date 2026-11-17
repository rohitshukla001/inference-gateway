package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

type stubRPS struct {
	calls int
	rps   int
	err   error
}

func (s *stubRPS) GetRPS(context.Context, string) (int, error) {
	s.calls++
	return s.rps, s.err
}

func TestCachedRepository_GetRPS(t *testing.T) {
	cases := map[string]*stubRPS{
		"row":    {rps: 60},
		"no row": {err: ErrNotFound},
	}
	for name, inner := range cases {
		t.Run(name, func(t *testing.T) {
			rds := goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()})
			c := &CachedRepository{inner: inner, rds: rds, ttl: time.Minute}
			for range 3 {
				got, err := c.GetRPS(t.Context(), "t1")
				if err != nil || got != inner.rps {
					t.Fatalf("got %d, %v; want %d", got, err, inner.rps)
				}
			}
			if inner.calls != 1 {
				t.Fatalf("inner calls = %d, want 1", inner.calls)
			}
		})
	}
}
