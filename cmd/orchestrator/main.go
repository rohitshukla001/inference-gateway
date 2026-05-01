// Orchestrator is the gRPC service that coordinates inference requests
// between the gateway and LLM providers.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rshuklas/inference-gateway/internal/budget"
	"github.com/rshuklas/inference-gateway/internal/cache/responsecache"
	"github.com/rshuklas/inference-gateway/internal/config"
	"github.com/rshuklas/inference-gateway/internal/db"
	"github.com/rshuklas/inference-gateway/internal/orchestrator"
	"github.com/rshuklas/inference-gateway/internal/orchestrator/grpcserver"
	"github.com/rshuklas/inference-gateway/internal/policy"
	"github.com/rshuklas/inference-gateway/internal/provider"
	"github.com/rshuklas/inference-gateway/internal/provider/mock"
	"github.com/rshuklas/inference-gateway/internal/provider/ollama"
	igredis "github.com/rshuklas/inference-gateway/internal/redis"
	"github.com/rshuklas/inference-gateway/internal/router"
	"github.com/rshuklas/inference-gateway/internal/usage"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.LoadOrchestratorConfig()

	log.Println("[orchestrator] starting...")
	log.Printf("[orchestrator] ollama endpoint: %s", cfg.OllamaEndpoint)
	log.Printf("[orchestrator] default model: %s", cfg.DefaultModel)
	log.Printf("[orchestrator] cache ttl: %s", cfg.CacheTTL)

	// Redis (response cache).
	rdsCtx, rdsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rdsCancel()
	rdsClient, err := igredis.Connect(rdsCtx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("[orchestrator] redis connect failed: %v", err)
	}
	defer rdsClient.Close()
	log.Println("[orchestrator] redis connected")

	cache := responsecache.New(rdsClient, cfg.CacheTTL)

	// Postgres (usage + policy).
	pgCtx, pgCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pgCancel()
	pool, err := db.Connect(pgCtx, cfg.PostgresURL)
	if err != nil {
		log.Fatalf("[orchestrator] postgres connect failed: %v", err)
	}
	defer pool.Close()
	log.Println("[orchestrator] postgres connected")

	usageRepo := usage.NewRepository(pool)
	policyRepo := policy.NewRepository(pool)
	budgetRepo := budget.NewRepository(pool)

	// Provider registry. Both providers are always available; the router
	// decides which one to call per request.
	providers := map[string]provider.Provider{
		"ollama": ollama.New(cfg.OllamaEndpoint, cfg.DefaultModel),
		"mock":   mock.New(),
	}
	log.Printf("[orchestrator] providers registered: %v", providerNames(providers))

	rtr := router.New(router.DefaultCatalog())

	svc := orchestrator.NewService(orchestrator.Deps{
		Providers:  providers,
		Cache:      cache,
		UsageRepo:  usageRepo,
		Router:     rtr,
		PolicyRepo: policyRepo,
		BudgetRepo: budgetRepo,
	})

	// gRPC server with tenant interceptor.
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(grpcserver.TenantUnaryInterceptor()),
	)
	pb.RegisterInferenceServiceServer(grpcServer, svc)
	reflection.Register(grpcServer)

	addr := fmt.Sprintf(":%s", cfg.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[orchestrator] failed to listen on %s: %v", addr, err)
	}

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

func providerNames(m map[string]provider.Provider) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	return names
}
