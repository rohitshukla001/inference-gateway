package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/messaging/natsclient"
)

type stubRepo struct {
	insertFn  func(ctx context.Context, tenantID string, payload []byte) (string, error)
	getByID   *Job
	getByErr  error
	insertHit int
}

func (s *stubRepo) Insert(ctx context.Context, tenantID string, payload []byte) (string, error) {
	s.insertHit++
	if s.insertFn != nil {
		return s.insertFn(ctx, tenantID, payload)
	}
	return "job-1", nil
}

func (s *stubRepo) GetByID(_ context.Context, _, _ string) (*Job, error) {
	if s.getByErr != nil {
		return nil, s.getByErr
	}
	return s.getByID, nil
}

type stubPublisher struct {
	subject string
	data    []byte
	err     error
	calls   int
}

func (p *stubPublisher) Publish(subject string, data []byte) error {
	p.calls++
	p.subject = subject
	p.data = data
	return p.err
}

func TestService_Submit_InsertsAndPublishes(t *testing.T) {
	repo := &stubRepo{}
	pub := &stubPublisher{}
	svc := NewService(repo, pub)

	jobID, err := svc.Submit(context.Background(), "tenant-1", []byte(`{"prompt":"hi"}`))
	if err != nil {
		t.Fatalf("submit returned error: %v", err)
	}
	if jobID != "job-1" {
		t.Errorf("expected job-1, got %q", jobID)
	}
	if repo.insertHit != 1 {
		t.Errorf("expected 1 insert, got %d", repo.insertHit)
	}
	if pub.calls != 1 {
		t.Errorf("expected 1 publish, got %d", pub.calls)
	}
	if pub.subject != natsclient.SubjectAsyncRequests {
		t.Errorf("expected subject %q, got %q", natsclient.SubjectAsyncRequests, pub.subject)
	}

	gotJob, gotTenant, err := DecodeQueueMessage(pub.data)
	if err != nil {
		t.Fatalf("decode published message: %v", err)
	}
	if gotJob != "job-1" || gotTenant != "tenant-1" {
		t.Errorf("payload mismatch: job=%q tenant=%q", gotJob, gotTenant)
	}
}

func TestService_Submit_PublishFailureReturnsJobIDAnyway(t *testing.T) {
	repo := &stubRepo{}
	pub := &stubPublisher{err: errors.New("nats down")}
	svc := NewService(repo, pub)

	jobID, err := svc.Submit(context.Background(), "tenant-1", []byte(`{}`))
	if err != nil {
		t.Fatalf("submit should not fail when publish fails: %v", err)
	}
	if jobID != "job-1" {
		t.Errorf("expected job-1, got %q", jobID)
	}
}

func TestService_Submit_InsertFailureBubblesUp(t *testing.T) {
	repo := &stubRepo{insertFn: func(context.Context, string, []byte) (string, error) {
		return "", errors.New("db down")
	}}
	pub := &stubPublisher{}
	svc := NewService(repo, pub)

	if _, err := svc.Submit(context.Background(), "tenant-1", []byte(`{}`)); err == nil {
		t.Fatal("expected error when insert fails")
	}
	if pub.calls != 0 {
		t.Error("publish should not run when insert fails")
	}
}

func TestService_Get_PassesThrough(t *testing.T) {
	want := &Job{JobID: "j1", TenantID: "t1", Status: StatusCompleted}
	repo := &stubRepo{getByID: want}
	svc := NewService(repo, &stubPublisher{})

	got, err := svc.Get(context.Background(), "t1", "j1")
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got != want {
		t.Errorf("expected pass-through, got %+v", got)
	}
}

func TestService_Get_NotFoundBubbles(t *testing.T) {
	repo := &stubRepo{getByErr: ErrNotFound}
	svc := NewService(repo, &stubPublisher{})

	_, err := svc.Get(context.Background(), "t1", "j1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDecodeQueueMessage(t *testing.T) {
	good, _ := json.Marshal(queueMessage{JobID: "j1", TenantID: "t1"})

	cases := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{"valid", good, false},
		{"missing job_id", []byte(`{"tenant_id":"t1"}`), true},
		{"missing tenant_id", []byte(`{"job_id":"j1"}`), true},
		{"malformed json", []byte(`not-json`), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DecodeQueueMessage(tc.data)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
