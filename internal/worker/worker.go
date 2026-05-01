// Package worker implements the async job consumer. It subscribes to the
// inference queue, claims pending jobs, executes them via the orchestrator's
// gRPC ExecuteInference RPC, and persists the result.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rshuklas/inference-gateway/internal/gateway"
	"github.com/rshuklas/inference-gateway/internal/jobs"
	"github.com/rshuklas/inference-gateway/internal/messaging/natsclient"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
)

// Worker consumes async inference jobs from NATS and persists their results.
type Worker struct {
	repo               *jobs.Repository
	natsConn           *nats.Conn
	orchestratorClient pb.InferenceServiceClient
	jobTimeout         time.Duration
}

// New constructs a Worker.
func New(
	repo *jobs.Repository,
	natsConn *nats.Conn,
	orchClient pb.InferenceServiceClient,
	jobTimeout time.Duration,
) *Worker {
	return &Worker{
		repo:               repo,
		natsConn:           natsConn,
		orchestratorClient: orchClient,
		jobTimeout:         jobTimeout,
	}
}

// Start subscribes to the async job subject and processes messages until ctx
// is cancelled. The subscription joins a queue group so multiple worker
// replicas split the load.
func (w *Worker) Start(ctx context.Context) error {
	sub, err := w.natsConn.QueueSubscribe(
		natsclient.SubjectAsyncRequests,
		natsclient.QueueGroupWorkers,
		func(msg *nats.Msg) {
			w.handle(ctx, msg)
		},
	)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	log.Printf("[worker] subscribed | subject=%s queue=%s",
		natsclient.SubjectAsyncRequests, natsclient.QueueGroupWorkers)

	<-ctx.Done()
	if err := sub.Drain(); err != nil {
		log.Printf("[worker] subscription drain error: %v", err)
	}
	return nil
}

// handle processes one queue message. Errors at this layer are logged and
// recorded against the job; we never NAK because redelivery races against
// the row-level Claim guard, which is the real correctness mechanism.
func (w *Worker) handle(parent context.Context, msg *nats.Msg) {
	jobID, tenantID, err := jobs.DecodeQueueMessage(msg.Data)
	if err != nil {
		log.Printf("[worker] dropping malformed message: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(parent, w.jobTimeout)
	defer cancel()

	_, requestPayload, claimed, err := w.repo.Claim(ctx, jobID)
	if err != nil {
		log.Printf("[worker] claim failed | job=%s err=%v", jobID, err)
		return
	}
	if !claimed {
		log.Printf("[worker] job already claimed (likely redelivery) | job=%s", jobID)
		return
	}

	log.Printf("[worker] claimed | job=%s tenant=%s", jobID, tenantID)

	var req gateway.ExecuteRequest
	if err := json.Unmarshal(requestPayload, &req); err != nil {
		w.fail(ctx, jobID, fmt.Sprintf("decode request: %v", err))
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
		w.fail(ctx, jobID, err.Error())
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
		w.fail(ctx, jobID, fmt.Sprintf("encode response: %v", err))
		return
	}

	if err := w.repo.CompleteSuccess(ctx, jobID, respPayload); err != nil {
		log.Printf("[worker] complete failed | job=%s err=%v", jobID, err)
		return
	}
	log.Printf("[worker] completed | job=%s model=%s", jobID, result.Model)
}

func (w *Worker) fail(ctx context.Context, jobID, errMsg string) {
	if err := w.repo.Fail(ctx, jobID, errMsg); err != nil {
		log.Printf("[worker] fail-write failed | job=%s err=%v", jobID, err)
	}
}

// RunSweeper periodically marks orphan running jobs as failed. Returns when
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
