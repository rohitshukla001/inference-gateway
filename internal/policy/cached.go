package policy

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// reader is the slice of *Repository that CachedRepository depends on.
// Defined as an interface so tests can stub the underlying store without
// needing a real Postgres pool.
type reader interface {
	Get(ctx context.Context, tenantID string) (*Policy, error)
}

// CachedRepository is a Redis read-through cache in front of a Repository.
// Cache misses fall through to Postgres and repopulate the cache. All cache
// failures are logged and treated as a miss so a Redis blip never fails a
// tenant lookup.
type CachedRepository struct {
	inner reader
	rds   *goredis.Client
	ttl   time.Duration
}

// NewCachedRepository wraps inner with a Redis-backed cache.
func NewCachedRepository(inner *Repository, rds *goredis.Client, ttl time.Duration) *CachedRepository {
	return &CachedRepository{inner: inner, rds: rds, ttl: ttl}
}

// Get returns the cached policy for tenant if present, otherwise loads from
// the underlying repository and primes the cache.
func (c *CachedRepository) Get(ctx context.Context, tenantID string) (*Policy, error) {
	key := "policy:" + tenantID
	if raw, err := c.rds.Get(ctx, key).Bytes(); err == nil {
		var p Policy
		if jerr := json.Unmarshal(raw, &p); jerr == nil {
			return &p, nil
		} else {
			log.Printf("[policy] cache decode failed (falling through): %v", jerr)
		}
	} else if !errors.Is(err, goredis.Nil) {
		log.Printf("[policy] cache get failed (falling through): %v", err)
	}

	p, err := c.inner.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	if raw, err := json.Marshal(p); err == nil {
		if serr := c.rds.Set(ctx, key, raw, c.ttl).Err(); serr != nil {
			log.Printf("[policy] cache set failed (non-fatal): %v", serr)
		}
	}
	return p, nil
}
