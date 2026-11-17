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

// SubjectAsyncRequests is the subject async inference jobs are published on.
const SubjectAsyncRequests = "inference.async.requests"

// StreamJobs is the JetStream stream that holds job messages until acked.
const StreamJobs = "INFERENCE_JOBS"

// ConsumerWorkers is the durable pull consumer shared by all worker replicas:
// each message goes to one worker and is redelivered if it is not acked.
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

// Connect dials NATS at url. It fails if the broker is unreachable at
// startup; after that it reconnects indefinitely.
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
