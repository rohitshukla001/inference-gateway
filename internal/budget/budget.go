// Package budget reads tenant spend caps and aggregates month-to-date
// usage cost so the orchestrator can gate calls before they reach a paid
// provider.
package budget

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means the tenant has no budget row. The orchestrator treats
// this as "no limit" so dev tenants without a row keep working.
var ErrNotFound = errors.New("tenant budget not found")

// Limits is a tenant's monthly spending caps.
type Limits struct {
	TenantID        string
	MonthlyLimitUSD float64
	SoftLimitUSD    float64
}

// Repository reads budgets and aggregates month-to-date usage.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires a Repository to the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const getLimitsSQL = `
SELECT tenant_id, monthly_limit_usd, soft_limit_usd
FROM tenant_budgets
WHERE tenant_id = $1
`

// GetLimits loads the configured caps for tenant.
func (r *Repository) GetLimits(ctx context.Context, tenantID string) (*Limits, error) {
	var l Limits
	err := r.pool.QueryRow(ctx, getLimitsSQL, tenantID).Scan(
		&l.TenantID,
		&l.MonthlyLimitUSD,
		&l.SoftLimitUSD,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get tenant budget: %w", err)
	}
	return &l, nil
}

const monthToDateSQL = `
SELECT COALESCE(SUM(cost), 0)
FROM usage_records
WHERE tenant_id = $1
  AND created_at >= date_trunc('month', NOW())
`

// MonthToDate returns the sum of usage_records.cost for tenant since the
// start of the current calendar month (server clock).
func (r *Repository) MonthToDate(ctx context.Context, tenantID string) (float64, error) {
	var spent float64
	err := r.pool.QueryRow(ctx, monthToDateSQL, tenantID).Scan(&spent)
	if err != nil {
		return 0, fmt.Errorf("month-to-date spend: %w", err)
	}
	return spent, nil
}
