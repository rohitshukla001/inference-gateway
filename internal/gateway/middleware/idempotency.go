package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/rshuklas/inference-gateway/internal/tenant"
)

// IdempotencyHeader is the request header that makes a call safely
// retryable within a tenant.
const IdempotencyHeader = "Idempotency-Key"

// inFlightTTL bounds how long a key stays claimed if the gateway dies
// mid-request without releasing it. It must outlast the slowest request.
const inFlightTTL = 5 * time.Minute

// idempotencyEntry is the stored value of a claimed key: InProgress plus the
// body fingerprint while the first request runs, then the response to replay.
// Headers are not stored, so internal headers never leak across requests.
type idempotencyEntry struct {
	InProgress  bool   `json:"in_progress,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Status      int    `json:"status,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

// Idempotency deduplicates requests per tenant by Idempotency-Key. It must
// run after Auth and MaxBody.
//
// Behaviour:
//   - No header, or no tenant in ctx: pass through.
//   - Key unused: claim it atomically (SET NX), run the handler, then store
//     the response if 2xx or release the key otherwise so the client can retry.
//   - Key used with a different request body: 422.
//   - Key claimed by a request still running: 409, so concurrent retries
//     never reach the provider twice.
//   - Key holds a stored response: replay status + body verbatim.
//
// Redis errors fail open (the request runs without deduplication).
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

			body, err := io.ReadAll(r.Body)
			if err != nil {
				var tooBig *http.MaxBytesError
				if errors.As(err, &tooBig) {
					writeIdemError(w, http.StatusRequestEntityTooLarge, "request body too large")
					return
				}
				writeIdemError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			sum := sha256.Sum256(body)
			fingerprint := hex.EncodeToString(sum[:])

			redisKey := "idem:" + t.ID + ":" + rawKey
			claim, _ := json.Marshal(idempotencyEntry{InProgress: true, Fingerprint: fingerprint})

			claimed, err := client.SetNX(r.Context(), redisKey, claim, inFlightTTL).Result()
			if err != nil {
				log.Printf("[gateway] idempotency claim failed (running without dedup): %v", err)
				next.ServeHTTP(w, r)
				return
			}

			if !claimed {
				entry, ok := loadEntry(r.Context(), client, redisKey)
				switch {
				case !ok:
					// Expired between SETNX and GET, or unreadable: ask for a
					// retry rather than risk a duplicate call.
					writeIdemError(w, http.StatusConflict, "idempotency key busy, retry")
				case entry.Fingerprint != fingerprint:
					writeIdemError(w, http.StatusUnprocessableEntity, "idempotency key reused with a different request body")
				case entry.InProgress:
					w.Header().Set("Retry-After", "1")
					writeIdemError(w, http.StatusConflict, "a request with this idempotency key is in progress")
				default:
					replay(w, entry)
				}
				return
			}

			rec := &captureWriter{ResponseWriter: w, status: http.StatusOK, body: &bytes.Buffer{}}
			next.ServeHTTP(rec, r)

			// Record the outcome even if the client already went away.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
			defer cancel()

			// Persist only 2xx; anything else releases the key so the
			// client can retry.
			if rec.status >= 200 && rec.status < 300 {
				storeEntry(ctx, client, redisKey, idempotencyEntry{
					Fingerprint: fingerprint,
					Status:      rec.status,
					Body:        rec.body.Bytes(),
				}, ttl)
				return
			}
			if err := client.Del(ctx, redisKey).Err(); err != nil {
				log.Printf("[gateway] idempotency release failed: %v", err)
			}
		})
	}
}

func writeIdemError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message, "code": code})
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

// captureWriter copies status and body for storage while still writing the
// response to the client.
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
