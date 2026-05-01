// Package tenant defines the tenant model, context helpers, and repository
// abstraction for resolving tenants from API keys.
package tenant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// ErrNotFound is returned when no tenant matches the supplied lookup.
var ErrNotFound = errors.New("tenant not found")

// Tenant represents an authenticated caller of the gateway.
type Tenant struct {
	ID        string
	Name      string
	Status    string
	Role      string
	CreatedAt time.Time
}

// HashAPIKey returns the lowercase hex sha256 of the raw key.
// API keys are never stored in plaintext; only the hash is persisted.
func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type ctxKey struct{}

// WithTenant returns a copy of ctx carrying t.
func WithTenant(ctx context.Context, t *Tenant) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// FromContext extracts the tenant attached to ctx, or nil if none.
func FromContext(ctx context.Context) *Tenant {
	t, _ := ctx.Value(ctxKey{}).(*Tenant)
	return t
}