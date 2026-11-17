// Package budget reads tenant spend caps and reserves month-to-date spend
// so the orchestrator can gate calls before they reach a paid provider.
package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means the tenant has no budget row; the orchestrator treats it
// as no limit.
var ErrNotFound = errors.New("tenant budget not found")

// ErrOverBudget means the call's projected cost does not fit in the
// tenant's remaining monthly budget.
var ErrOverBudget = errors.New("monthly budget exceeded")

// OverBudgetError is ErrOverBudget with the tenant's month-to-date spend.
type OverBudgetError struct {
	SpentUSD float64
}

func (e *OverBudgetError) Error() string { return ErrOverBudget.Error() }

// Unwrap lets errors.Is(err, ErrOverBudget) match.
func (e *OverBudgetError) Unwrap() error { return ErrOverBudget }

// Limits is a tenant's monthly spending caps.
type Limits struct {
	TenantID        string
	MonthlyLimitUSD float64
	SoftLimitUSD    float64
}

// Repository reads budgets and maintains month-to-date spend.
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

// Reservation is a projected cost held against a tenant's monthly spend.
// Settle it exactly once with the call's actual cost.
type Reservation struct {
	TenantID string
	Month    time.Time
	Amount   float64
	// SpentUSD is the month-to-date spend including this reservation.
	SpentUSD float64
}

const reserveSQL = `
INSERT INTO tenant_monthly_spend AS s (tenant_id, month, spent_usd)
VALUES ($1, date_trunc('month', NOW())::date, $2)
ON CONFLICT (tenant_id, month) DO UPDATE
SET spent_usd = s.spent_usd + EXCLUDED.spent_usd
WHERE s.spent_usd + EXCLUDED.spent_usd <= $3
RETURNING month, spent_usd
`

const monthToDateSQL = `
SELECT COALESCE(
    (SELECT spent_usd FROM tenant_monthly_spend
     WHERE tenant_id = $1 AND month = date_trunc('month', NOW())::date),
    0)
`

// Reserve adds amount to the tenant's current-month spend if the total stays
// within limit, else returns an OverBudgetError with current spend. The check
// and increment are one statement, so concurrent callers cannot both claim
// the same headroom.
func (r *Repository) Reserve(ctx context.Context, tenantID string, amount, limit float64) (*Reservation, error) {
	if amount > limit {
		return nil, r.overBudget(ctx, tenantID)
	}
	res := Reservation{TenantID: tenantID, Amount: amount}
	err := r.pool.QueryRow(ctx, reserveSQL, tenantID, amount, limit).Scan(&res.Month, &res.SpentUSD)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, r.overBudget(ctx, tenantID)
		}
		return nil, fmt.Errorf("reserve budget: %w", err)
	}
	return &res, nil
}

func (r *Repository) overBudget(ctx context.Context, tenantID string) error {
	var spent float64
	if err := r.pool.QueryRow(ctx, monthToDateSQL, tenantID).Scan(&spent); err != nil {
		return fmt.Errorf("%w (month-to-date unavailable: %v)", ErrOverBudget, err)
	}
	return &OverBudgetError{SpentUSD: spent}
}

const settleSQL = `
UPDATE tenant_monthly_spend
SET spent_usd = spent_usd + $3
WHERE tenant_id = $1 AND month = $2
`

// Settle replaces a reservation's projected amount with the actual cost
// (zero if the call failed). It adjusts the month the reservation was made
// in, even if the call finished after a month boundary.
func (r *Repository) Settle(ctx context.Context, res *Reservation, actual float64) error {
	if _, err := r.pool.Exec(ctx, settleSQL, res.TenantID, res.Month, actual-res.Amount); err != nil {
		return fmt.Errorf("settle budget: %w", err)
	}
	return nil
}
