// Package middleware contains gateway HTTP middleware (auth, idempotency, etc.).
package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/rshuklas/inference-gateway/internal/tenant"
)

// TenantResolver looks up a tenant by raw API key. It's the minimal contract
// the auth middleware needs, which keeps tests independent of Postgres.
type TenantResolver interface {
	GetByAPIKey(ctx context.Context, rawKey string) (*tenant.Tenant, error)
}

// Auth returns a middleware that enforces Bearer-token API key auth and
// attaches the resolved tenant to the request context.
func Auth(resolver TenantResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeAuthError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
				return
			}

			t, err := resolver.GetByAPIKey(r.Context(), rawKey)
			if err != nil {
				if errors.Is(err, tenant.ErrNotFound) {
					writeAuthError(w, http.StatusUnauthorized, "invalid api key")
					return
				}
				log.Printf("[gateway] tenant lookup failed: %v", err)
				writeAuthError(w, http.StatusInternalServerError, "auth backend error")
				return
			}

			next.ServeHTTP(w, r.WithContext(tenant.WithTenant(r.Context(), t)))
		})
	}
}

// bearerToken parses an "Authorization: Bearer <key>" header and returns the
// raw key. It accepts case-insensitive scheme matching to be lenient with
// clients.
func bearerToken(header string) (string, bool) {
	const scheme = "bearer"
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 {
		return "", false
	}
	if !strings.EqualFold(parts[0], scheme) {
		return "", false
	}
	key := strings.TrimSpace(parts[1])
	if key == "" {
		return "", false
	}
	return key, true
}

func writeAuthError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": message,
		"code":  code,
	})
}