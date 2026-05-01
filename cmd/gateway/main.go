// Gateway is the HTTP entry point for the Inference Gateway.
// It exposes a REST API and delegates inference calls to the orchestrator via gRPC.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rshuklas/inference-gateway/internal/config"
	"github.com/rshuklas/inference-gateway/internal/db"
	"github.com/rshuklas/inference-gateway/internal/gateway"
	"github.com/rshuklas/inference-gateway/internal/gateway/grpcclient"
	"github.com/rshuklas/inference-gateway/internal/gateway/middleware"
	igredis "github.com/rshuklas/inference-gateway/internal/redis"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	cfg := config.LoadGatewayConfig()

	log.Println("[gateway] starting...")
	log.Printf("[gateway] connecting to orchestrator at %s", cfg.OrchestratorGRPCAddr)
	log.Printf("[gateway] idempotency ttl: %s", cfg.IdempotencyTTL)

	// Migrate schema before opening the long-lived pool.
	log.Println("[gateway] running migrations...")
	if err := db.Migrate(cfg.PostgresURL); err != nil {
		log.Fatalf("[gateway] migration failed: %v", err)
	}
	log.Println("[gateway] migrations applied")

	// Postgres pool for tenant lookups.
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	pool, err := db.Connect(dbCtx, cfg.PostgresURL)
	if err != nil {
		log.Fatalf("[gateway] postgres connect failed: %v", err)
	}
	defer pool.Close()
	log.Println("[gateway] postgres connected")

	tenantRepo := tenant.NewRepository(pool)

	// Redis for idempotency replay.
	rdsCtx, rdsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rdsCancel()

	rdsClient, err := igredis.Connect(rdsCtx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("[gateway] redis connect failed: %v", err)
	}
	defer rdsClient.Close()
	log.Println("[gateway] redis connected")

	// gRPC client to orchestrator with tenant-propagating interceptor.
	conn, err := grpc.NewClient(
		cfg.OrchestratorGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(grpcclient.TenantUnaryInterceptor()),
	)
	if err != nil {
		log.Fatalf("[gateway] failed to connect to orchestrator: %v", err)
	}
	defer conn.Close()

	client := pb.NewInferenceServiceClient(conn)
	handler := gateway.NewHandler(client)

	authMW := middleware.Auth(tenantRepo)
	idempotencyMW := middleware.Idempotency(rdsClient, cfg.IdempotencyTTL)

	mux := http.NewServeMux()
	// Health is intentionally unauthenticated.
	mux.HandleFunc("/health", handler.HandleHealth)
	// Order matters: auth attaches the tenant to ctx; idempotency reads it.
	mux.Handle(
		"/v1/inference/execute",
		authMW(idempotencyMW(http.HandlerFunc(handler.HandleExecute))),
	)

	addr := fmt.Sprintf(":%s", cfg.HTTPPort)
	server := &http.Server{Addr: addr, Handler: mux}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("[gateway] shutting down...")
		server.Close()
	}()

	log.Printf("[gateway] listening on %s", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("[gateway] server error: %v", err)
	}

	log.Println("[gateway] stopped")
}
