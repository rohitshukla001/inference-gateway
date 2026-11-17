// Package policy reads per-tenant routing constraints: allowed models and
// the preferred tier.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a tenant has no policy row.
var ErrNotFound = errors.New("tenant policy not found")

// Policy is a tenant's routing constraints.
type Policy struct {
	TenantID      string
	AllowedModels []string
	DefaultTier   string
}

// AllowsModel reports whether model is in AllowedModels. An empty list
// allows all models.
func (p *Policy) AllowsModel(model string) bool {
	if len(p.AllowedModels) == 0 {
		return true
	}
	return slices.Contains(p.AllowedModels, model)
}

// Repository reads tenant policies from Postgres.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires a Repository to the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const getSQL = `
SELECT tenant_id, allowed_models, default_tier
FROM tenant_policies
WHERE tenant_id = $1
`

// Get loads the policy for a tenant.
func (r *Repository) Get(ctx context.Context, tenantID string) (*Policy, error) {
	var (
		p       Policy
		rawJSON []byte
	)
	err := r.pool.QueryRow(ctx, getSQL, tenantID).Scan(
		&p.TenantID,
		&rawJSON,
		&p.DefaultTier,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get tenant policy: %w", err)
	}

	if err := json.Unmarshal(rawJSON, &p.AllowedModels); err != nil {
		return nil, fmt.Errorf("decode allowed_models: %w", err)
	}

	return &p, nil
}
