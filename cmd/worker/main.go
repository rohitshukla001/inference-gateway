// Worker consumes async jobs from NATS JetStream, runs them through the
// orchestrator over gRPC, and persists the result.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/rshuklas/inference-gateway/internal/config"
	"github.com/rshuklas/inference-gateway/internal/db"
	"github.com/rshuklas/inference-gateway/internal/gateway/grpcclient"
	"github.com/rshuklas/inference-gateway/internal/jobs"
	"github.com/rshuklas/inference-gateway/internal/messaging/natsclient"
	"github.com/rshuklas/inference-gateway/internal/observability/tracing"
	"github.com/rshuklas/inference-gateway/internal/worker"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	cfg := config.LoadWorkerConfig()
	if cfg.InternalAuthToken == "" {
		log.Fatal("[worker] INTERNAL_AUTH_TOKEN must be set")
	}

	log.Println("[worker] starting...")
	log.Printf("[worker] orchestrator: %s", cfg.OrchestratorGRPCAddr)
	log.Printf("[worker] nats: %s", cfg.NATSURL)
	log.Printf("[worker] job timeout: %s concurrency: %d sweeper: every %s threshold %s",
		cfg.JobTimeout, cfg.Concurrency, cfg.SweepInterval, cfg.StuckJobThreshold)

	traceShutdown, err := tracing.Init(context.Background(), cfg.OTLPEndpoint, "worker", "0.6.0")
	if err != nil {
		log.Printf("[worker] tracing init failed (continuing without): %v", err)
		traceShutdown = func(context.Context) error { return nil }
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = traceShutdown(ctx)
	}()

	pgCtx, pgCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pgCancel()
	pool, dbErr := db.Connect(pgCtx, cfg.PostgresURL)
	if dbErr != nil {
		log.Fatalf("[worker] postgres connect failed: %v", dbErr)
	}
	defer pool.Close()
	log.Println("[worker] postgres connected")

	natsConn, natsErr := natsclient.Connect(cfg.NATSURL)
	if natsErr != nil {
		log.Fatalf("[worker] nats connect failed: %v", natsErr)
	}
	defer natsConn.Close()
	log.Println("[worker] nats connected")

	js, err := jetstream.New(natsConn)
	if err != nil {
		log.Fatalf("[worker] jetstream init failed: %v", err)
	}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer setupCancel()
	if err := natsclient.EnsureStream(setupCtx, js); err != nil {
		log.Fatalf("[worker] %v", err)
	}
	// AckWait covers a message waiting in the client buffer for a free slot
	// (up to one job) plus its own run, so healthy jobs are never redelivered.
	consumer, err := js.CreateOrUpdateConsumer(setupCtx, natsclient.StreamJobs, jetstream.ConsumerConfig{
		Durable:       natsclient.ConsumerWorkers,
		FilterSubject: natsclient.SubjectAsyncRequests,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       2*cfg.JobTimeout + 30*time.Second,
		MaxDeliver:    5,
	})
	if err != nil {
		log.Fatalf("[worker] create consumer failed: %v", err)
	}

	conn, dialErr := grpc.NewClient(
		cfg.OrchestratorGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithChainUnaryInterceptor(
			grpcclient.InternalAuthUnaryInterceptor(cfg.InternalAuthToken),
			grpcclient.TenantUnaryInterceptor(),
		),
	)
	if dialErr != nil {
		log.Fatalf("[worker] orchestrator dial failed: %v", dialErr)
	}
	defer conn.Close()
	orchClient := pb.NewInferenceServiceClient(conn)

	repo := jobs.NewRepository(pool)
	w := worker.New(repo, consumer, orchClient, cfg.JobTimeout, cfg.Concurrency)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := w.Start(ctx); err != nil {
			log.Printf("[worker] subscriber error: %v", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		worker.RunSweeper(ctx, repo, cfg.SweepInterval, cfg.StuckJobThreshold)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[worker] shutting down...")
	cancel()

	wg.Wait()
	log.Println("[worker] stopped")
}
