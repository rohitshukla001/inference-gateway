// Package usage records per-request token, latency, and cost data to Postgres.
package usage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Record is a single inference call's billable footprint.
type Record struct {
	TenantID     string
	Model        string
	Provider     string
	InputTokens  int32
	OutputTokens int32
	LatencyMs    int64
	Cost         float64
	Cached       bool
}

// Repository writes usage records to Postgres.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires a Repository to the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const insertSQL = `
INSERT INTO usage_records (
    tenant_id, model, provider, input_tokens, output_tokens, latency_ms, cost, cached
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
`

// Insert persists rec. Records without a TenantID are rejected.
func (r *Repository) Insert(ctx context.Context, rec Record) error {
	if rec.TenantID == "" {
		return fmt.Errorf("usage record missing tenant id")
	}
	_, err := r.pool.Exec(
		ctx,
		insertSQL,
		rec.TenantID,
		rec.Model,
		rec.Provider,
		rec.InputTokens,
		rec.OutputTokens,
		rec.LatencyMs,
		rec.Cost,
		rec.Cached,
	)
	if err != nil {
		return fmt.Errorf("insert usage record: %w", err)
	}
	return nil
}
