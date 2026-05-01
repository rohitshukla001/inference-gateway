// Package natsclient wraps NATS connection construction. The wrapper is thin
// on purpose; callers use *nats.Conn directly for Publish/Subscribe so the
// full NATS API stays available.
package natsclient

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// SubjectAsyncRequests is the NATS subject the gateway publishes to and the
// worker consumes from for async inference jobs.
const SubjectAsyncRequests = "inference.async.requests"

// QueueGroupWorkers is the queue group name workers join so each message is
// delivered to exactly one worker, even when multiple replicas run.
const QueueGroupWorkers = "inference-workers"

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
