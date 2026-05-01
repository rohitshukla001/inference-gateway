package ratelimit

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newTestLimiter(t *testing.T) (*Limiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rds := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	return New(rds), mr
}

func TestAllow_ZeroRPSIsUnlimited(t *testing.T) {
	l, _ := newTestLimiter(t)

	for range 100 {
		d, err := l.Allow(t.Context(), "t1", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !d.Allowed {
			t.Fatal("expected unlimited rps to always allow")
		}
	}
}

func TestAllow_AllowsUpToCapAndDeniesOver(t *testing.T) {
	l, _ := newTestLimiter(t)
	const rps = 3

	for i := range rps {
		d, err := l.Allow(t.Context(), "t1", rps)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("call %d should be allowed within cap", i)
		}
		if d.Remaining != rps-i-1 {
			t.Errorf("call %d: expected remaining=%d, got %d", i, rps-i-1, d.Remaining)
		}
	}

	d, err := l.Allow(t.Context(), "t1", rps)
	if err != nil {
		t.Fatalf("over-cap call: %v", err)
	}
	if d.Allowed {
		t.Error("expected over-cap call to be denied")
	}
	if d.RetryAfter <= 0 || d.RetryAfter > time.Second {
		t.Errorf("retry-after %s not in (0, 1s]", d.RetryAfter)
	}
}

func TestAllow_BucketResetsOnNewSecond(t *testing.T) {
	l, mr := newTestLimiter(t)
	const rps = 2

	for range rps {
		d, _ := l.Allow(t.Context(), "t1", rps)
		if !d.Allowed {
			t.Fatal("expected allowed within cap")
		}
	}
	if d, _ := l.Allow(t.Context(), "t1", rps); d.Allowed {
		t.Fatal("expected denied at cap boundary")
	}

	mr.FastForward(2 * time.Second)

	d, err := l.Allow(t.Context(), "t1", rps)
	if err != nil {
		t.Fatalf("post-fastforward: %v", err)
	}
	if !d.Allowed {
		t.Error("expected allowed after bucket TTL")
	}
}

func TestAllow_PerTenantIsolation(t *testing.T) {
	l, _ := newTestLimiter(t)
	const rps = 1

	if d, _ := l.Allow(t.Context(), "tenant-a", rps); !d.Allowed {
		t.Fatal("tenant-a first call should be allowed")
	}
	if d, _ := l.Allow(t.Context(), "tenant-a", rps); d.Allowed {
		t.Fatal("tenant-a second call should be denied")
	}
	if d, _ := l.Allow(t.Context(), "tenant-b", rps); !d.Allowed {
		t.Fatal("tenant-b should not be affected by tenant-a's bucket")
	}
}
