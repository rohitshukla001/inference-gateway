// Orchestrator is the gRPC service that coordinates inference requests
// between the gateway and LLM providers.
package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rshuklas/inference-gateway/internal/config"
	"github.com/rshuklas/inference-gateway/internal/orchestrator"
	"github.com/rshuklas/inference-gateway/internal/provider/ollama"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.LoadOrchestratorConfig()

	log.Println("[orchestrator] starting...")
	log.Printf("[orchestrator] ollama endpoint: %s", cfg.OllamaEndpoint)
	log.Printf("[orchestrator] default model: %s", cfg.DefaultModel)

	// Initialize Ollama provider
	ollamaProvider := ollama.New(cfg.OllamaEndpoint, cfg.DefaultModel)

	// Create orchestrator service
	svc := orchestrator.NewService(ollamaProvider)

	// Create gRPC server
	grpcServer := grpc.NewServer()
	pb.RegisterInferenceServiceServer(grpcServer, svc)

	// Enable gRPC reflection for debugging with grpcurl
	reflection.Register(grpcServer)

	// Start listening
	addr := fmt.Sprintf(":%s", cfg.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[orchestrator] failed to listen on %s: %v", addr, err)
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("[orchestrator] shutting down...")
		grpcServer.GracefulStop()
	}()

	log.Printf("[orchestrator] gRPC server listening on %s", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("[orchestrator] server error: %v", err)
	}

	log.Println("[orchestrator] stopped")
}
