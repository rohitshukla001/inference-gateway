// Worker is the async job consumer for the Inference Gateway. It pulls
// pending jobs off NATS, executes them via the orchestrator's gRPC API,
// and persists the result.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rshuklas/inference-gateway/internal/config"
	"github.com/rshuklas/inference-gateway/internal/db"
	"github.com/rshuklas/inference-gateway/internal/gateway/grpcclient"
	"github.com/rshuklas/inference-gateway/internal/jobs"
	"github.com/rshuklas/inference-gateway/internal/messaging/natsclient"
	"github.com/rshuklas/inference-gateway/internal/worker"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	cfg := config.LoadWorkerConfig()

	log.Println("[worker] starting...")
	log.Printf("[worker] orchestrator: %s", cfg.OrchestratorGRPCAddr)
	log.Printf("[worker] nats: %s", cfg.NATSURL)
	log.Printf("[worker] job timeout: %s sweeper: every %s threshold %s",
		cfg.JobTimeout, cfg.SweepInterval, cfg.StuckJobThreshold)

	pgCtx, pgCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pgCancel()
	pool, err := db.Connect(pgCtx, cfg.PostgresURL)
	if err != nil {
		log.Fatalf("[worker] postgres connect failed: %v", err)
	}
	defer pool.Close()
	log.Println("[worker] postgres connected")

	natsConn, err := natsclient.Connect(cfg.NATSURL)
	if err != nil {
		log.Fatalf("[worker] nats connect failed: %v", err)
	}
	defer natsConn.Close()
	log.Println("[worker] nats connected")

	conn, err := grpc.NewClient(
		cfg.OrchestratorGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(grpcclient.TenantUnaryInterceptor()),
	)
	if err != nil {
		log.Fatalf("[worker] orchestrator dial failed: %v", err)
	}
	defer conn.Close()
	orchClient := pb.NewInferenceServiceClient(conn)

	repo := jobs.NewRepository(pool)
	w := worker.New(repo, natsConn, orchClient, cfg.JobTimeout)

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
