package ratelimit

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// rpsReader is the slice of *Repository that CachedRepository depends on.
type rpsReader interface {
	GetRPS(ctx context.Context, tenantID string) (int, error)
}

// CachedRepository is a Redis read-through cache in front of a Repository
// so the rate-limit cap is not read from Postgres on every request. A
// tenant without a row is cached as 0 (unlimited), which is how callers
// already treat ErrNotFound.
type CachedRepository struct {
	inner rpsReader
	rds   *goredis.Client
	ttl   time.Duration
}

// NewCachedRepository wraps inner with a Redis-backed cache.
func NewCachedRepository(inner *Repository, rds *goredis.Client, ttl time.Duration) *CachedRepository {
	return &CachedRepository{inner: inner, rds: rds, ttl: ttl}
}

// GetRPS returns the cached cap for tenantID if present, otherwise loads it
// and primes the cache. Cache failures are logged and treated as a miss.
func (c *CachedRepository) GetRPS(ctx context.Context, tenantID string) (int, error) {
	key := "rps:" + tenantID
	if raw, err := c.rds.Get(ctx, key).Result(); err == nil {
		if rps, perr := strconv.Atoi(raw); perr == nil {
			return rps, nil
		}
	} else if !errors.Is(err, goredis.Nil) {
		log.Printf("[ratelimit] cache get failed (falling through): %v", err)
	}

	rps, err := c.inner.GetRPS(ctx, tenantID)
	if errors.Is(err, ErrNotFound) {
		rps, err = 0, nil
	}
	if err != nil {
		return 0, err
	}

	if serr := c.rds.Set(ctx, key, rps, c.ttl).Err(); serr != nil {
		log.Printf("[ratelimit] cache set failed (non-fatal): %v", serr)
	}
	return rps, nil
}
