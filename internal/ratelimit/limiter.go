// Package ratelimit implements a per-tenant request limiter backed by Redis.
//
// It is a fixed-window counter per second: each request increments
// rl:{tenant}:{epoch_second} and is allowed while the count is within rps.
// Simpler than a sliding window, at the cost of allowing up to 2x rps in a
// burst straddling a second boundary; acceptable for a coarse cap.
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

// Allow counts the request in the current second's bucket and checks it
// against rps; rps <= 0 means unlimited. Buckets expire after 2 seconds.
func (l *Limiter) Allow(ctx context.Context, tenantID string, rps int) (Decision, error) {
	if rps <= 0 {
		return Decision{Allowed: true}, nil
	}

	now := time.Now()
	bucket := now.Unix()
	key := fmt.Sprintf("rl:%s:%d", tenantID, bucket)

	// INCR and EXPIRE share one MULTI/EXEC so a bucket never lacks a TTL.
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
