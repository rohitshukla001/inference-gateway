package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/rshuklas/inference-gateway/internal/ratelimit"
	"github.com/rshuklas/inference-gateway/internal/tenant"
)

// RateLimitRepo is the slice of ratelimit.Repository the middleware needs.
// Defined here so tests can stub the Postgres lookup.
type RateLimitRepo interface {
	GetRPS(ctx context.Context, tenantID string) (int, error)
}

// RateLimitChecker is the slice of ratelimit.Limiter the middleware needs.
type RateLimitChecker interface {
	Allow(ctx context.Context, tenantID string, rps int) (ratelimit.Decision, error)
}

// RateLimit returns a middleware that enforces per-tenant request caps. It
// must run after Auth (so a tenant is on the context). A tenant with no
// rate-limit row is treated as unlimited; both repo and Redis errors fail
// open (allow the request) so a transient infra blip can't black-hole
// traffic.
func RateLimit(repo RateLimitRepo, limiter RateLimitChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t := tenant.FromContext(r.Context())
			if t == nil {
				next.ServeHTTP(w, r)
				return
			}

			rps, err := repo.GetRPS(r.Context(), t.ID)
			if err != nil {
				if !errors.Is(err, ratelimit.ErrNotFound) {
					log.Printf("[gateway] ratelimit lookup failed (allowing): %v", err)
				}
				next.ServeHTTP(w, r)
				return
			}

			decision, err := limiter.Allow(r.Context(), t.ID, rps)
			if err != nil {
				log.Printf("[gateway] ratelimit check failed (allowing): %v", err)
				next.ServeHTTP(w, r)
				return
			}

			if !decision.Allowed {
				retrySec := max(int(decision.RetryAfter.Seconds()), 1)
				w.Header().Set("Retry-After", strconv.Itoa(retrySec))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": "rate limit exceeded",
					"code":  http.StatusTooManyRequests,
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
