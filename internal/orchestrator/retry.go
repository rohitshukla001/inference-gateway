package orchestrator

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

// RetryConfig controls per-provider retries. MaxAttempts includes the first
// attempt; 0 or 1 disables retries.
type RetryConfig struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// executeWithRetry runs p.Execute with exponential backoff and full jitter.
// Context cancellation and errors provider.IsRetryable rejects (NonRetryable
// errors such as bad requests, and timeouts) return at once so the caller can
// try the fallback; other errors are retried in case the blip is transient.
func (s *Service) executeWithRetry(
	ctx context.Context,
	p provider.Provider,
	req *provider.InferenceRequest,
) (*provider.InferenceResponse, error) {
	attempts := max(s.retry.MaxAttempts, 1)

	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			delay := backoffDelay(attempt-1, s.retry.BaseBackoff, s.retry.MaxBackoff)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		result, err := p.Execute(ctx, req)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return nil, err
		}
		if !provider.IsRetryable(err) {
			return nil, err
		}

		if attempt < attempts-1 {
			log.Printf("[orchestrator] provider %s attempt %d/%d failed (will retry): %v",
				p.Name(), attempt+1, attempts, err)
		}
	}
	return nil, fmt.Errorf("exhausted %d attempts: %w", attempts, lastErr)
}

// backoffDelay returns a full-jitter delay in [0, min(base<<attempt,
// maxBackoff)). attempt is zero-indexed.
func backoffDelay(attempt int, base, maxBackoff time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	cap := base << attempt
	if cap <= 0 || cap > maxBackoff {
		cap = maxBackoff
	}
	if cap <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(cap)))
}
