package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/nats-io/nats.go"
	"github.com/rshuklas/inference-gateway/internal/messaging/natsclient"
)

// Publisher is the subset of the NATS API the jobs service needs. Defined as
// an interface so tests can swap in an in-memory fake without spinning up a
// broker.
type Publisher interface {
	Publish(subject string, data []byte) error
}

// repoStore is the subset of repository operations the service needs. It
// keeps the service decoupled from Postgres for unit tests.
type repoStore interface {
	Insert(ctx context.Context, tenantID string, requestPayload []byte) (string, error)
	GetByID(ctx context.Context, tenantID, jobID string) (*Job, error)
}

// Service is the gateway-facing facade over the jobs repository and the
// outbound queue. Submit creates a row and publishes a notification; Get
// loads a row scoped to the calling tenant.
type Service struct {
	repo      repoStore
	publisher Publisher
}

// NewService wires a Service to its repository and queue publisher.
func NewService(repo repoStore, publisher Publisher) *Service {
	return &Service{repo: repo, publisher: publisher}
}

// queueMessage is the wire payload published to the worker queue. It is a
// pointer to the row, not the row itself, so the worker re-reads the latest
// state from Postgres before doing any work (avoids stale data on
// redelivery).
type queueMessage struct {
	JobID    string `json:"job_id"`
	TenantID string `json:"tenant_id"`
}

// Submit inserts a new pending job and publishes a notification to the
// worker queue. If the publish fails after the row is committed, the row
// stays pending and the sweeper will eventually time it out; the caller
// still gets a job_id so the API contract holds.
func (s *Service) Submit(ctx context.Context, tenantID string, requestPayload []byte) (string, error) {
	jobID, err := s.repo.Insert(ctx, tenantID, requestPayload)
	if err != nil {
		return "", fmt.Errorf("submit: %w", err)
	}

	msg, err := json.Marshal(queueMessage{JobID: jobID, TenantID: tenantID})
	if err != nil {
		return "", fmt.Errorf("marshal queue message: %w", err)
	}

	if err := s.publisher.Publish(natsclient.SubjectAsyncRequests, msg); err != nil {
		log.Printf("[jobs] publish failed for job=%s (row stays pending, sweeper will reap): %v", jobID, err)
	}

	return jobID, nil
}

// Get returns the job, scoped to tenantID. Returns ErrNotFound if the row is
// missing or belongs to a different tenant.
func (s *Service) Get(ctx context.Context, tenantID, jobID string) (*Job, error) {
	return s.repo.GetByID(ctx, tenantID, jobID)
}

// DecodeQueueMessage parses the wire payload the worker received off NATS.
func DecodeQueueMessage(data []byte) (jobID, tenantID string, err error) {
	var m queueMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return "", "", fmt.Errorf("decode queue message: %w", err)
	}
	if m.JobID == "" || m.TenantID == "" {
		return "", "", fmt.Errorf("queue message missing job_id or tenant_id")
	}
	return m.JobID, m.TenantID, nil
}

// natsPublisher adapts *nats.Conn to the Publisher interface.
type natsPublisher struct {
	conn *nats.Conn
}

// NewNATSPublisher returns a Publisher backed by a live NATS connection.
func NewNATSPublisher(conn *nats.Conn) Publisher {
	return &natsPublisher{conn: conn}
}

func (p *natsPublisher) Publish(subject string, data []byte) error {
	return p.conn.Publish(subject, data)
}