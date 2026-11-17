// Orchestrator is the gRPC service that coordinates inference requests
// between the gateway and LLM providers.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rshuklas/inference-gateway/internal/budget"
	"github.com/rshuklas/inference-gateway/internal/cache/responsecache"
	"github.com/rshuklas/inference-gateway/internal/config"
	"github.com/rshuklas/inference-gateway/internal/db"
	"github.com/rshuklas/inference-gateway/internal/observability/metrics"
	"github.com/rshuklas/inference-gateway/internal/observability/tracing"
	"github.com/rshuklas/inference-gateway/internal/orchestrator"
	"github.com/rshuklas/inference-gateway/internal/orchestrator/grpcserver"
	"github.com/rshuklas/inference-gateway/internal/policy"
	"github.com/rshuklas/inference-gateway/internal/provider"
	"github.com/rshuklas/inference-gateway/internal/provider/health"
	"github.com/rshuklas/inference-gateway/internal/provider/mock"
	"github.com/rshuklas/inference-gateway/internal/provider/ollama"
	igredis "github.com/rshuklas/inference-gateway/internal/redis"
	"github.com/rshuklas/inference-gateway/internal/router"
	"github.com/rshuklas/inference-gateway/internal/usage"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.LoadOrchestratorConfig()
	if cfg.InternalAuthToken == "" {
		log.Fatal("[orchestrator] INTERNAL_AUTH_TOKEN must be set")
	}

	log.Println("[orchestrator] starting...")
	log.Printf("[orchestrator] ollama endpoint: %s", cfg.OllamaEndpoint)
	log.Printf("[orchestrator] default model: %s", cfg.DefaultModel)
	log.Printf("[orchestrator] ollama timeout: %s", cfg.OllamaTimeout)
	log.Printf("[orchestrator] cache ttl: %s", cfg.CacheTTL)
	log.Printf("[orchestrator] retry: attempts=%d base=%s max=%s",
		cfg.RetryMaxAttempts, cfg.RetryBaseBackoff, cfg.RetryMaxBackoff)
	log.Printf("[orchestrator] health: interval=%s timeout=%s",
		cfg.HealthInterval, cfg.HealthTimeout)
	log.Printf("[orchestrator] config cache ttl: %s", cfg.ConfigCacheTTL)

	traceShutdown, err := tracing.Init(context.Background(), cfg.OTLPEndpoint, "orchestrator", "0.6.0")
	if err != nil {
		log.Printf("[orchestrator] tracing init failed (continuing without): %v", err)
		traceShutdown = func(context.Context) error { return nil }
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = traceShutdown(ctx)
	}()

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
	policyRepo := policy.NewCachedRepository(policy.NewRepository(pool), rdsClient, cfg.ConfigCacheTTL)
	budgetRepo := budget.NewCachedRepository(budget.NewRepository(pool), rdsClient, cfg.ConfigCacheTTL)

	// Provider registry. Both providers are always available; the router
	// decides which one to call per request.
	providers := map[string]provider.Provider{
		"ollama": ollama.New(cfg.OllamaEndpoint, cfg.DefaultModel, cfg.OllamaTimeout),
		"mock":   mock.New(),
	}
	log.Printf("[orchestrator] providers registered: %v", providerNames(providers))

	rtr := router.New(router.DefaultCatalog())

	mtr := metrics.New()

	healthChecker := health.New(providers, cfg.HealthInterval, cfg.HealthTimeout)

	svc := orchestrator.NewService(orchestrator.Deps{
		Providers:  providers,
		Cache:      cache,
		UsageRepo:  usageRepo,
		Router:     rtr,
		PolicyRepo: policyRepo,
		BudgetRepo: budgetRepo,
		Metrics:    mtr,
		Retry: orchestrator.RetryConfig{
			MaxAttempts: cfg.RetryMaxAttempts,
			BaseBackoff: cfg.RetryBaseBackoff,
			MaxBackoff:  cfg.RetryMaxBackoff,
		},
		Health: healthChecker,
	})

	// gRPC server with auth + tenant + tracing interceptors. The otelgrpc
	// handler has to come first so the interceptors see a span-bearing
	// context. Internal auth runs before tenant extraction so x-tenant-id
	// is only trusted from authenticated callers.
	grpcServer := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			grpcserver.InternalAuthUnaryInterceptor(cfg.InternalAuthToken),
			grpcserver.TenantUnaryInterceptor(),
		),
	)
	pb.RegisterInferenceServiceServer(grpcServer, svc)
	if cfg.EnableReflection {
		reflection.Register(grpcServer)
	}

	addr := fmt.Sprintf(":%s", cfg.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[orchestrator] failed to listen on %s: %v", addr, err)
	}

	adminCtx, adminCancel := context.WithCancel(context.Background())
	defer adminCancel()

	healthChecker.Start(adminCtx)

	adminExtras := map[string]http.Handler{
		"/health/providers": healthChecker.Handler(),
	}

	go func() {
		if err := metrics.RunAdminServer(adminCtx, mtr, fmt.Sprintf(":%s", cfg.AdminPort), adminExtras); err != nil {
			log.Printf("[orchestrator] admin server error: %v", err)
		}
	}()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("[orchestrator] shutting down...")
		adminCancel()
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
