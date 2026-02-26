// Gateway is the HTTP entry point for the Inference Gateway.
// It exposes a REST API and delegates inference calls to the orchestrator via gRPC.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/rshuklas/inference-gateway/internal/config"
	"github.com/rshuklas/inference-gateway/internal/gateway"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	cfg := config.LoadGatewayConfig()

	log.Println("[gateway] starting...")
	log.Printf("[gateway] connecting to orchestrator at %s", cfg.OrchestratorGRPCAddr)

	// Establish gRPC connection to orchestrator
	conn, err := grpc.NewClient(
		cfg.OrchestratorGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("[gateway] failed to connect to orchestrator: %v", err)
	}
	defer conn.Close()

	// Create gRPC client
	client := pb.NewInferenceServiceClient(conn)

	// Create HTTP handler
	handler := gateway.NewHandler(client)

	// Register routes
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handler.HandleHealth)
	mux.HandleFunc("/v1/inference/execute", handler.HandleExecute)

	// Start HTTP server
	addr := fmt.Sprintf(":%s", cfg.HTTPPort)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Graceful shutdown
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
