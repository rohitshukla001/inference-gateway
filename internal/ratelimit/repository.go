package ratelimit

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a tenant has no rate-limit row. Callers treat
// it as no limit.
var ErrNotFound = errors.New("tenant rate limit not found")

// Repository reads per-tenant RPS caps from Postgres.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires a Repository to the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const getRPSSQL = `
SELECT rps
FROM tenant_rate_limits
WHERE tenant_id = $1
`

// GetRPS loads the per-second cap for tenantID. An rps of 0 means unlimited.
func (r *Repository) GetRPS(ctx context.Context, tenantID string) (int, error) {
	var rps int
	err := r.pool.QueryRow(ctx, getRPSSQL, tenantID).Scan(&rps)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("get tenant rate limit: %w", err)
	}
	return rps, nil
}
