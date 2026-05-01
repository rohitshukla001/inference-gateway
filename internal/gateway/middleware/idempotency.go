package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/rshuklas/inference-gateway/internal/tenant"
	goredis "github.com/redis/go-redis/v9"
)

// IdempotencyHeader is the request header clients set to make a write call
// safely retryable. Identical (tenant, key) pairs return the cached response.
const IdempotencyHeader = "Idempotency-Key"

// idempotencyEntry is the on-wire shape of a replayed response. Headers are
// intentionally omitted: only status + body round-trip, which is enough for
// JSON APIs and avoids leaking server-internal headers across requests.
type idempotencyEntry struct {
	Status int    `json:"status"`
	Body   []byte `json:"body"`
}

// Idempotency returns middleware that deduplicates identical requests within
// a tenant scope using the Idempotency-Key header.
//
// Behaviour:
//   - No header on the request: pass through, no-op.
//   - No tenant in ctx: pass through (auth middleware should have rejected first).
//   - Stored entry exists: replay status + body verbatim, skip the next handler.
//   - No stored entry: invoke next, capture the response, persist if 2xx.
func Idempotency(client *goredis.Client, ttl time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := r.Header.Get(IdempotencyHeader)
			if rawKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			t := tenant.FromContext(r.Context())
			if t == nil {
				next.ServeHTTP(w, r)
				return
			}

			redisKey := "idem:" + t.ID + ":" + rawKey

			if entry, ok := loadEntry(r.Context(), client, redisKey); ok {
				replay(w, entry)
				return
			}

			rec := &captureWriter{ResponseWriter: w, status: http.StatusOK, body: &bytes.Buffer{}}
			next.ServeHTTP(rec, r)

			// Only persist successful responses. Replaying a 5xx would punish
			// retries; replaying a 4xx is debatable but keeps surprises low.
			if rec.status >= 200 && rec.status < 300 {
				storeEntry(r.Context(), client, redisKey, idempotencyEntry{
					Status: rec.status,
					Body:   rec.body.Bytes(),
				}, ttl)
			}
		})
	}
}

func loadEntry(ctx context.Context, client *goredis.Client, key string) (idempotencyEntry, bool) {
	raw, err := client.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, goredis.Nil) {
			log.Printf("[gateway] idempotency get failed: %v", err)
		}
		return idempotencyEntry{}, false
	}
	var entry idempotencyEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		log.Printf("[gateway] idempotency decode failed: %v", err)
		return idempotencyEntry{}, false
	}
	return entry, true
}

func storeEntry(ctx context.Context, client *goredis.Client, key string, entry idempotencyEntry, ttl time.Duration) {
	raw, err := json.Marshal(entry)
	if err != nil {
		log.Printf("[gateway] idempotency encode failed: %v", err)
		return
	}
	if err := client.Set(ctx, key, raw, ttl).Err(); err != nil {
		log.Printf("[gateway] idempotency set failed: %v", err)
	}
}

func replay(w http.ResponseWriter, entry idempotencyEntry) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Idempotent-Replay", "true")
	w.WriteHeader(entry.Status)
	_, _ = w.Write(entry.Body)
}

// captureWriter buffers status + body so the middleware can persist them
// after the handler completes, while still streaming the original response
// to the client.
type captureWriter struct {
	http.ResponseWriter
	status      int
	body        *bytes.Buffer
	wroteHeader bool
}

func (c *captureWriter) WriteHeader(code int) {
	c.status = code
	c.wroteHeader = true
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.wroteHeader = true
	}
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}
