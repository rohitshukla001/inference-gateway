package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/rshuklas/inference-gateway/internal/messaging/natsclient"
)

// Publisher publishes job messages. Publish returns once the broker has
// persisted the message.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

// repoStore is the subset of Repository the service needs.
type repoStore interface {
	Insert(ctx context.Context, tenantID string, requestPayload []byte) (string, error)
	GetByID(ctx context.Context, tenantID, jobID string) (*Job, error)
	Fail(ctx context.Context, jobID string, errMsg string) error
}

// Service is the gateway-facing facade over the jobs repository and queue.
type Service struct {
	repo      repoStore
	publisher Publisher
}

// NewService wires a Service to its repository and queue publisher.
func NewService(repo repoStore, publisher Publisher) *Service {
	return &Service{repo: repo, publisher: publisher}
}

// queueMessage is the queue payload. It only references the row, so the
// worker always reads current state from Postgres, even on redelivery.
type queueMessage struct {
	JobID    string `json:"job_id"`
	TenantID string `json:"tenant_id"`
}

// Submit inserts a pending job and enqueues it. If the publish fails, the job
// is marked failed and an error returned, so the client never gets a job_id
// no worker will see.
func (s *Service) Submit(ctx context.Context, tenantID string, requestPayload []byte) (string, error) {
	jobID, err := s.repo.Insert(ctx, tenantID, requestPayload)
	if err != nil {
		return "", fmt.Errorf("submit: %w", err)
	}

	msg, err := json.Marshal(queueMessage{JobID: jobID, TenantID: tenantID})
	if err != nil {
		return "", fmt.Errorf("marshal queue message: %w", err)
	}

	if err := s.publisher.Publish(ctx, natsclient.SubjectAsyncRequests, msg); err != nil {
		if ferr := s.repo.Fail(context.WithoutCancel(ctx), jobID, "enqueue failed"); ferr != nil {
			log.Printf("[jobs] marking unpublished job=%s failed: %v (sweeper will reap)", jobID, ferr)
		}
		return "", fmt.Errorf("publish job %s: %w", jobID, err)
	}

	return jobID, nil
}

// Get returns a job scoped to tenantID, or ErrNotFound.
func (s *Service) Get(ctx context.Context, tenantID, jobID string) (*Job, error) {
	return s.repo.GetByID(ctx, tenantID, jobID)
}

// DecodeQueueMessage parses a queue payload into its job and tenant IDs.
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

// jetStreamPublisher adapts a JetStream context to the Publisher interface.
type jetStreamPublisher struct {
	js jetstream.JetStream
}

// NewJetStreamPublisher returns a Publisher backed by JetStream.
func NewJetStreamPublisher(js jetstream.JetStream) Publisher {
	return &jetStreamPublisher{js: js}
}

func (p *jetStreamPublisher) Publish(ctx context.Context, subject string, data []byte) error {
	_, err := p.js.Publish(ctx, subject, data)
	return err
}
