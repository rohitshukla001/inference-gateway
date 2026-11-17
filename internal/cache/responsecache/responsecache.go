// Package responsecache provides a Redis-backed exact-match cache for
// inference responses, keyed per tenant on a hash of the model + prompt.
package responsecache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Cache wraps a Redis client with a fixed TTL for response entries.
type Cache struct {
	client *goredis.Client
	ttl    time.Duration
}

// New returns a Cache that stores entries with the given TTL.
func New(client *goredis.Client, ttl time.Duration) *Cache {
	return &Cache{client: client, ttl: ttl}
}

// Key derives the Redis key for a tenant, model, and prompt. The tenant ID is
// part of the key so entries never cross tenants.
func Key(tenantID, model, prompt string) string {
	sum := sha256.Sum256([]byte(model + ":" + prompt))
	return fmt.Sprintf("cache:%s:%s", tenantID, hex.EncodeToString(sum[:]))
}

// Get decodes the entry at key into dst and reports whether it was a hit. A
// miss is not an error.
func (c *Cache) Get(ctx context.Context, key string, dst any) (bool, error) {
	raw, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return false, nil
		}
		return false, fmt.Errorf("redis get: %w", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, fmt.Errorf("decode cache entry: %w", err)
	}
	return true, nil
}

// Set stores value under key with the cache TTL.
func (c *Cache) Set(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode cache entry: %w", err)
	}
	if err := c.client.Set(ctx, key, raw, c.ttl).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}
	return nil
}
