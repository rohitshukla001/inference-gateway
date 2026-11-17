// Package natsclient wraps NATS connection construction and the JetStream
// stream that carries async inference jobs.
package natsclient

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// SubjectAsyncRequests is the NATS subject the gateway publishes to and the
// worker consumes from for async inference jobs.
const SubjectAsyncRequests = "inference.async.requests"

// StreamJobs is the JetStream stream that persists async job messages until
// a worker acknowledges them.
const StreamJobs = "INFERENCE_JOBS"

// ConsumerWorkers is the durable pull consumer all worker replicas share, so
// each message is delivered to one worker at a time and redelivered if that
// worker dies before acknowledging it.
const ConsumerWorkers = "inference-workers"

// EnsureStream creates or updates the jobs stream. Work-queue retention
// deletes each message once a worker acknowledges it.
func EnsureStream(ctx context.Context, js jetstream.JetStream) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamJobs,
		Subjects:  []string{SubjectAsyncRequests},
		Retention: jetstream.WorkQueuePolicy,
		Storage:   jetstream.FileStorage,
	})
	if err != nil {
		return fmt.Errorf("ensure stream %s: %w", StreamJobs, err)
	}
	return nil
}

// Connect opens a connection to NATS at url and waits for the initial
// handshake. It returns an error if the broker is unreachable.
func Connect(url string) (*nats.Conn, error) {
	nc, err := nats.Connect(
		url,
		nats.Name("inference-gateway"),
		nats.Timeout(5*time.Second),
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	return nc, nil
}
