// Package worker implements the async job consumer. It pulls job messages
// from a JetStream consumer, claims pending jobs, executes them via the
// orchestrator's gRPC ExecuteInference RPC, persists the result, and only
// then acknowledges the message.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/rshuklas/inference-gateway/internal/gateway"
	"github.com/rshuklas/inference-gateway/internal/jobs"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
)

// resultWriteTimeout bounds the final job-status write, which runs on its
// own context so it succeeds even after the job's deadline has passed.
const resultWriteTimeout = 5 * time.Second

// jobStore is the slice of jobs.Repository the worker needs.
type jobStore interface {
	Claim(ctx context.Context, jobID string) (tenantID string, requestPayload []byte, claimed bool, err error)
	CompleteSuccess(ctx context.Context, jobID string, responsePayload []byte) error
	Fail(ctx context.Context, jobID string, errMsg string) error
}

// queueMsg is the slice of jetstream.Msg the worker needs.
type queueMsg interface {
	Data() []byte
	Ack() error
	Nak() error
	Term() error
}

// Worker consumes async inference jobs and persists their results.
type Worker struct {
	repo               jobStore
	consumer           jetstream.Consumer
	orchestratorClient pb.InferenceServiceClient
	jobTimeout         time.Duration
	concurrency        int
}

// New constructs a Worker that runs up to concurrency jobs at once.
func New(
	repo jobStore,
	consumer jetstream.Consumer,
	orchClient pb.InferenceServiceClient,
	jobTimeout time.Duration,
	concurrency int,
) *Worker {
	return &Worker{
		repo:               repo,
		consumer:           consumer,
		orchestratorClient: orchClient,
		jobTimeout:         jobTimeout,
		concurrency:        max(concurrency, 1),
	}
}

// Start pulls and processes messages until ctx is cancelled, then stops
// pulling and waits for in-flight jobs to finish. Jobs run on their own
// contexts (bounded by jobTimeout) so a shutdown does not abort them.
// Messages fetched but not yet started are redelivered after AckWait.
func (w *Worker) Start(ctx context.Context) error {
	iter, err := w.consumer.Messages(jetstream.PullMaxMessages(w.concurrency))
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	stop := context.AfterFunc(ctx, iter.Stop)
	defer stop()
	log.Printf("[worker] consuming | concurrency=%d", w.concurrency)

	slots := make(chan struct{}, w.concurrency)
	var wg sync.WaitGroup
	for {
		// Take a slot before pulling so we never hold more messages than
		// we can run.
		slots <- struct{}{}
		msg, err := iter.Next()
		if err != nil {
			<-slots
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				break
			}
			log.Printf("[worker] next message: %v", err)
			continue
		}
		wg.Go(func() {
			defer func() { <-slots }()
			w.handle(msg)
		})
	}

	log.Printf("[worker] draining in-flight jobs")
	wg.Wait()
	return nil
}

// handle processes one message. The message is acknowledged once the job
// reaches a terminal state (or is found already claimed); a transient
// failure to claim naks it for redelivery; a malformed message is dropped.
func (w *Worker) handle(msg queueMsg) {
	jobID, tenantID, err := jobs.DecodeQueueMessage(msg.Data())
	if err != nil {
		log.Printf("[worker] dropping malformed message: %v", err)
		_ = msg.Term()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), w.jobTimeout)
	defer cancel()

	_, requestPayload, claimed, err := w.repo.Claim(ctx, jobID)
	if err != nil {
		log.Printf("[worker] claim failed (will redeliver) | job=%s err=%v", jobID, err)
		_ = msg.Nak()
		return
	}
	if !claimed {
		log.Printf("[worker] job already claimed (likely redelivery) | job=%s", jobID)
		_ = msg.Ack()
		return
	}
	defer func() { _ = msg.Ack() }()

	log.Printf("[worker] claimed | job=%s tenant=%s", jobID, tenantID)

	var req gateway.ExecuteRequest
	if err := json.Unmarshal(requestPayload, &req); err != nil {
		w.fail(jobID, fmt.Sprintf("decode request: %v", err))
		return
	}

	rpcCtx := tenant.WithTenant(ctx, &tenant.Tenant{ID: tenantID})

	resp, err := w.orchestratorClient.ExecuteInference(rpcCtx, &pb.ExecuteInferenceRequest{
		Prompt:       req.Prompt,
		Model:        req.Model,
		Provider:     req.Provider,
		MaxCost:      req.MaxCost,
		LatencySlaMs: req.LatencySLAMs,
	})
	if err != nil {
		log.Printf("[worker] orchestrator error | job=%s err=%v", jobID, err)
		// Store the same client-safe message the sync API would return,
		// not the raw error, which can carry provider internals.
		_, msg := gateway.MapGRPCError(err)
		w.fail(jobID, msg)
		return
	}

	result := gateway.ExecuteResponse{
		Output:   resp.GetOutput(),
		Model:    resp.GetModel(),
		Provider: resp.GetProvider(),
		Cached:   resp.GetCached(),
		Cost:     resp.GetCost(),
	}
	if u := resp.GetUsage(); u != nil {
		result.Usage = &gateway.UsageInfo{
			PromptTokens:     u.GetPromptTokens(),
			CompletionTokens: u.GetCompletionTokens(),
			TotalTokens:      u.GetTotalTokens(),
		}
	}

	respPayload, err := json.Marshal(result)
	if err != nil {
		w.fail(jobID, fmt.Sprintf("encode response: %v", err))
		return
	}

	wctx, wcancel := context.WithTimeout(context.Background(), resultWriteTimeout)
	defer wcancel()
	if err := w.repo.CompleteSuccess(wctx, jobID, respPayload); err != nil {
		log.Printf("[worker] complete failed | job=%s err=%v", jobID, err)
		return
	}
	log.Printf("[worker] completed | job=%s model=%s", jobID, result.Model)
}

func (w *Worker) fail(jobID, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), resultWriteTimeout)
	defer cancel()
	if err := w.repo.Fail(ctx, jobID, errMsg); err != nil {
		log.Printf("[worker] fail-write failed | job=%s err=%v", jobID, err)
	}
}

// RunSweeper periodically marks orphan pending and running jobs as failed. Returns when
// ctx is cancelled.
func RunSweeper(ctx context.Context, repo *jobs.Repository, interval, threshold time.Duration) {
	if interval <= 0 || threshold <= 0 {
		log.Printf("[worker] sweeper disabled (interval=%s threshold=%s)", interval, threshold)
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	log.Printf("[worker] sweeper running | interval=%s threshold=%s", interval, threshold)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := repo.SweepStale(ctx, threshold)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				log.Printf("[worker] sweep error: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("[worker] swept %d stuck jobs", n)
			}
		}
	}
}
