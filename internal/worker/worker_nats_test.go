package worker

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc"
)

// slowOrch takes d per call and counts calls running at the same time.
type slowOrch struct {
	fakeOrch
	d             time.Duration
	running, peak atomic.Int32
	done          atomic.Int32
}

func (o *slowOrch) ExecuteInference(ctx context.Context, _ *pb.ExecuteInferenceRequest, _ ...grpc.CallOption) (*pb.ExecuteInferenceResponse, error) {
	n := o.running.Add(1)
	for {
		p := o.peak.Load()
		if n <= p || o.peak.CompareAndSwap(p, n) {
			break
		}
	}
	defer o.running.Add(-1)
	select {
	case <-time.After(o.d):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	o.done.Add(1)
	return &pb.ExecuteInferenceResponse{Output: "ok"}, nil
}

// TestStart_ConcurrentAndDrainsOnShutdown runs against the docker-compose
// NATS (JetStream enabled) and is skipped when it is not reachable.
func TestStart_ConcurrentAndDrainsOnShutdown(t *testing.T) {
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := nats.Connect(url, nats.Timeout(time.Second))
	if err != nil {
		t.Skipf("nats not reachable, skipping: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := fmt.Sprint(time.Now().UnixNano())
	stream, subject := "TEST_JOBS_"+id, "test.jobs."+id
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: stream, Subjects: []string{subject}, Retention: jetstream.WorkQueuePolicy}); err != nil {
		t.Skipf("jetstream unavailable, skipping: %v", err)
	}
	t.Cleanup(func() { _ = js.DeleteStream(context.Background(), stream) })
	cons, err := js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		Durable: "w", AckPolicy: jetstream.AckExplicitPolicy, AckWait: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	const jobs, concurrency = 8, 4
	for range jobs {
		if _, err := js.Publish(ctx, subject, []byte(`{"job_id":"j","tenant_id":"t1"}`)); err != nil {
			t.Fatal(err)
		}
	}

	orch := &slowOrch{d: 300 * time.Millisecond}
	store := &fakeStore{claimed: true, payload: []byte(`{"prompt":"hi"}`)}
	w := New(store, cons, orch, 10*time.Second, concurrency)

	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { _ = w.Start(runCtx); close(stopped) }()

	// Let the first batch start, then shut down mid-flight.
	deadline := time.Now().Add(5 * time.Second)
	for orch.running.Load() < concurrency && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-stopped

	if p := orch.peak.Load(); p != concurrency {
		t.Errorf("peak concurrency = %d, want %d", p, concurrency)
	}
	if orch.running.Load() != 0 {
		t.Errorf("Start returned with %d jobs still running", orch.running.Load())
	}
	started := int(orch.done.Load())
	if started < concurrency || len(store.completed) != started || len(store.failed) != 0 {
		t.Errorf("done=%d completed=%d failed=%d: in-flight jobs must finish, not fail", started, len(store.completed), len(store.failed))
	}

	// Everything not processed must still be in the stream for the next
	// worker. Flush so the server has seen our acks.
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	info, err := cons.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if remaining := int(info.NumPending) + info.NumAckPending; remaining != jobs-started {
		t.Errorf("remaining in stream = %d, want %d", remaining, jobs-started)
	}
}
