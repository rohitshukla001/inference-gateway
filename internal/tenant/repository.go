package tenant

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository looks up tenants in Postgres.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires a Repository to the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const lookupByKeyHashSQL = `
SELECT t.tenant_id, t.name, t.status, k.role, t.created_at
FROM api_keys k
JOIN tenants t ON t.tenant_id = k.tenant_id
WHERE k.key_hash = $1
  AND t.status = 'active'
LIMIT 1
`

// GetByAPIKey resolves the tenant that owns the given raw API key.
// Returns ErrNotFound if the key is unknown or the tenant is not active.
func (r *Repository) GetByAPIKey(ctx context.Context, rawKey string) (*Tenant, error) {
	hash := HashAPIKey(rawKey)

	var t Tenant
	err := r.pool.QueryRow(ctx, lookupByKeyHashSQL, hash).Scan(
		&t.ID,
		&t.Name,
		&t.Status,
		&t.Role,
		&t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lookup tenant by api key: %w", err)
	}

	return &t, nil
}
