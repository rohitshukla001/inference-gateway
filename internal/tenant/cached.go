package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// keyResolver is the slice of *Repository that CachedRepository depends on.
type keyResolver interface {
	GetByAPIKey(ctx context.Context, rawKey string) (*Tenant, error)
}

// CachedRepository is a Redis read-through cache over Repository so auth
// skips Postgres on most requests. Only successful lookups are cached, keyed
// by the key hash, never the raw key. A revoked key or deactivated tenant
// keeps working for up to ttl, so keep ttl short.
type CachedRepository struct {
	inner keyResolver
	rds   *goredis.Client
	ttl   time.Duration
}

// NewCachedRepository wraps inner with a Redis-backed cache.
func NewCachedRepository(inner *Repository, rds *goredis.Client, ttl time.Duration) *CachedRepository {
	return &CachedRepository{inner: inner, rds: rds, ttl: ttl}
}

// GetByAPIKey returns the cached tenant for rawKey, or loads and caches it.
// Redis errors are logged and treated as a miss.
func (c *CachedRepository) GetByAPIKey(ctx context.Context, rawKey string) (*Tenant, error) {
	key := "apikey:" + HashAPIKey(rawKey)
	if raw, err := c.rds.Get(ctx, key).Bytes(); err == nil {
		var t Tenant
		if jerr := json.Unmarshal(raw, &t); jerr == nil {
			return &t, nil
		} else {
			log.Printf("[tenant] cache decode failed (falling through): %v", jerr)
		}
	} else if !errors.Is(err, goredis.Nil) {
		log.Printf("[tenant] cache get failed (falling through): %v", err)
	}

	t, err := c.inner.GetByAPIKey(ctx, rawKey)
	if err != nil {
		return nil, err
	}

	if raw, err := json.Marshal(t); err == nil {
		if serr := c.rds.Set(ctx, key, raw, c.ttl).Err(); serr != nil {
			log.Printf("[tenant] cache set failed (non-fatal): %v", serr)
		}
	}
	return t, nil
}
