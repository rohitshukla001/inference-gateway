// Package middleware contains the gateway's HTTP middleware.
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

// TenantResolver looks up a tenant by raw API key.
type TenantResolver interface {
	GetByAPIKey(ctx context.Context, rawKey string) (*tenant.Tenant, error)
}

// Auth enforces Bearer API key auth and puts the resolved tenant on the
// request context.
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

// bearerToken extracts the key from an "Authorization: Bearer <key>" header.
// The scheme is matched case-insensitively.
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