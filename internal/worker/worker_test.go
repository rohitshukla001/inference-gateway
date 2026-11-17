package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc"
)

type fakeMsg struct {
	data                 []byte
	acked, naked, termed int
}

func (m *fakeMsg) Data() []byte { return m.data }
func (m *fakeMsg) Ack() error   { m.acked++; return nil }
func (m *fakeMsg) Nak() error   { m.naked++; return nil }
func (m *fakeMsg) Term() error  { m.termed++; return nil }

type fakeStore struct {
	mu        sync.Mutex
	claimErr  error
	claimed   bool
	payload   []byte
	completed []string
	failed    []string
}

func (s *fakeStore) Claim(context.Context, string) (string, []byte, bool, error) {
	return "t1", s.payload, s.claimed, s.claimErr
}

func (s *fakeStore) CompleteSuccess(ctx context.Context, jobID string, _ []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.completed = append(s.completed, jobID)
	return nil
}

func (s *fakeStore) Fail(ctx context.Context, jobID, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.failed = append(s.failed, jobID)
	return nil
}

type fakeOrch struct {
	calls  int
	err    error
	tenant string
}

func (o *fakeOrch) ExecuteInference(ctx context.Context, _ *pb.ExecuteInferenceRequest, _ ...grpc.CallOption) (*pb.ExecuteInferenceResponse, error) {
	o.calls++
	if t := tenant.FromContext(ctx); t != nil {
		o.tenant = t.ID
	}
	if o.err != nil {
		return nil, o.err
	}
	return &pb.ExecuteInferenceResponse{Output: "ok", Model: "m"}, nil
}

func (o *fakeOrch) HealthCheck(context.Context, *pb.HealthCheckRequest, ...grpc.CallOption) (*pb.HealthCheckResponse, error) {
	return &pb.HealthCheckResponse{}, nil
}

func queueData(t *testing.T) []byte {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"job_id": "j1", "tenant_id": "t1"})
	return b
}

func TestHandle(t *testing.T) {
	cases := []struct {
		name                      string
		data                      []byte
		store                     *fakeStore
		orchErr                   error
		wantAck, wantNak          int
		wantTerm, wantCalls       int
		wantCompleted, wantFailed int
	}{
		{name: "malformed message is dropped", data: []byte("{"), store: &fakeStore{}, wantTerm: 1},
		{name: "claim error is redelivered", store: &fakeStore{claimErr: errors.New("db down")}, wantNak: 1},
		{name: "already claimed is acked without running", store: &fakeStore{}, wantAck: 1},
		{name: "success completes and acks", store: &fakeStore{claimed: true, payload: []byte(`{"prompt":"hi"}`)},
			wantAck: 1, wantCalls: 1, wantCompleted: 1},
		{name: "orchestrator error fails and acks", store: &fakeStore{claimed: true, payload: []byte(`{"prompt":"hi"}`)},
			orchErr: errors.New("boom"), wantAck: 1, wantCalls: 1, wantFailed: 1},
		{name: "bad payload fails and acks", store: &fakeStore{claimed: true, payload: []byte(`{`)},
			wantAck: 1, wantFailed: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := c.data
			if data == nil {
				data = queueData(t)
			}
			msg := &fakeMsg{data: data}
			orch := &fakeOrch{err: c.orchErr}
			w := New(c.store, nil, orch, time.Second, 1)
			w.handle(msg)

			if msg.acked != c.wantAck || msg.naked != c.wantNak || msg.termed != c.wantTerm {
				t.Errorf("ack/nak/term = %d/%d/%d, want %d/%d/%d", msg.acked, msg.naked, msg.termed, c.wantAck, c.wantNak, c.wantTerm)
			}
			if orch.calls != c.wantCalls {
				t.Errorf("orchestrator calls = %d, want %d", orch.calls, c.wantCalls)
			}
			if c.wantCalls > 0 && orch.tenant != "t1" {
				t.Errorf("tenant on rpc ctx = %q, want t1", orch.tenant)
			}
			if len(c.store.completed) != c.wantCompleted || len(c.store.failed) != c.wantFailed {
				t.Errorf("completed/failed = %v/%v", c.store.completed, c.store.failed)
			}
		})
	}
}

// A job whose orchestrator call hit the job deadline must still get its
// failure recorded.
func TestHandle_RecordsFailureAfterJobDeadline(t *testing.T) {
	store := &fakeStore{claimed: true, payload: []byte(`{"prompt":"hi"}`)}
	orch := &fakeOrch{err: context.DeadlineExceeded}
	w := New(store, nil, orch, time.Nanosecond, 1)
	time.Sleep(time.Millisecond)
	w.handle(&fakeMsg{data: queueData(t)})
	if len(store.failed) != 1 {
		t.Fatalf("failure not recorded: %v", store.failed)
	}
}
