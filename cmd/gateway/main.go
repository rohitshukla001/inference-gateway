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
	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	cfg := config.LoadGatewayConfig()

	log.Println("[gateway] starting...")
	log.Printf("[gateway] connecting to orchestrator at %s", cfg.OrchestratorGRPCAddr)

	// Run migrations before opening the long-lived pool.
	log.Println("[gateway] running migrations...")
	if err := db.Migrate(cfg.PostgresURL); err != nil {
		log.Fatalf("[gateway] migration failed: %v", err)
	}
	log.Println("[gateway] migrations applied")

	// Connect Postgres pool for tenant lookups.
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	pool, err := db.Connect(dbCtx, cfg.PostgresURL)
	if err != nil {
		log.Fatalf("[gateway] postgres connect failed: %v", err)
	}
	defer pool.Close()
	log.Println("[gateway] postgres connected")

	tenantRepo := tenant.NewRepository(pool)

	// Establish gRPC connection to orchestrator with tenant-propagating interceptor.
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

	authMiddleware := middleware.Auth(tenantRepo)

	mux := http.NewServeMux()
	// Health is intentionally unauthenticated - load balancers and humans need it.
	mux.HandleFunc("/health", handler.HandleHealth)
	mux.Handle("/v1/inference/execute", authMiddleware(http.HandlerFunc(handler.HandleExecute)))

	addr := fmt.Sprintf(":%s", cfg.HTTPPort)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

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