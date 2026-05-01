package budget

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// reader is the slice of *Repository that CachedRepository depends on.
// Defined as an interface so tests can stub the underlying store.
type reader interface {
	GetLimits(ctx context.Context, tenantID string) (*Limits, error)
	MonthToDate(ctx context.Context, tenantID string) (float64, error)
}

// CachedRepository is a Redis read-through cache in front of a Repository.
// Only GetLimits is cached - MonthToDate is a live aggregation that must
// reflect the latest usage_records and is intentionally pass-through.
type CachedRepository struct {
	inner reader
	rds   *goredis.Client
	ttl   time.Duration
}

// NewCachedRepository wraps inner with a Redis-backed cache.
func NewCachedRepository(inner *Repository, rds *goredis.Client, ttl time.Duration) *CachedRepository {
	return &CachedRepository{inner: inner, rds: rds, ttl: ttl}
}

// GetLimits returns the cached limits for tenant if present, otherwise loads
// from the underlying repository and primes the cache.
func (c *CachedRepository) GetLimits(ctx context.Context, tenantID string) (*Limits, error) {
	key := "budget:" + tenantID
	if raw, err := c.rds.Get(ctx, key).Bytes(); err == nil {
		var l Limits
		if jerr := json.Unmarshal(raw, &l); jerr == nil {
			return &l, nil
		} else {
			log.Printf("[budget] cache decode failed (falling through): %v", jerr)
		}
	} else if !errors.Is(err, goredis.Nil) {
		log.Printf("[budget] cache get failed (falling through): %v", err)
	}

	l, err := c.inner.GetLimits(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	if raw, err := json.Marshal(l); err == nil {
		if serr := c.rds.Set(ctx, key, raw, c.ttl).Err(); serr != nil {
			log.Printf("[budget] cache set failed (non-fatal): %v", serr)
		}
	}
	return l, nil
}

// MonthToDate is forwarded directly to the underlying repository. It is not
// cached because spending changes per-request.
func (c *CachedRepository) MonthToDate(ctx context.Context, tenantID string) (float64, error) {
	return c.inner.MonthToDate(ctx, tenantID)
}
