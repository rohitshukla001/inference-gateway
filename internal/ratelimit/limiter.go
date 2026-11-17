// Package ratelimit implements a per-tenant request limiter backed by Redis.
//
// The algorithm is a fixed-window counter at 1-second granularity: each
// request increments rl:{tenant}:{epoch_second} and is allowed if the
// resulting count stays within the configured rps. This is simpler than a
// true sliding window and accurate enough for the v1 demo; the trade-off is
// burstiness at second boundaries, which is acceptable for a coarse cap.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Limiter caps requests per second per tenant against a Redis instance.
type Limiter struct {
	rds *goredis.Client
}

// New returns a Limiter that talks to rds.
func New(rds *goredis.Client) *Limiter {
	return &Limiter{rds: rds}
}

// Decision is the result of a rate-limit check.
type Decision struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
}

// Allow checks the current second's bucket for tenantID against rps. An rps
// of 0 means unlimited and always returns Allowed=true. The bucket key has a
// 2-second TTL, set atomically with the increment, so old buckets evict on
// their own.
func (l *Limiter) Allow(ctx context.Context, tenantID string, rps int) (Decision, error) {
	if rps <= 0 {
		return Decision{Allowed: true}, nil
	}

	now := time.Now()
	bucket := now.Unix()
	key := fmt.Sprintf("rl:%s:%d", tenantID, bucket)

	// INCR and EXPIRE run in one MULTI/EXEC so a bucket can never be left
	// without a TTL.
	pipe := l.rds.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 2*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return Decision{}, fmt.Errorf("ratelimit incr: %w", err)
	}
	count := incr.Val()

	if count > int64(rps) {
		retry := max(time.Until(time.Unix(bucket+1, 0)), 0)
		return Decision{Allowed: false, Remaining: 0, RetryAfter: retry}, nil
	}
	return Decision{Allowed: true, Remaining: rps - int(count)}, nil
}
