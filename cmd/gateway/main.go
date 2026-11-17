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

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rshuklas/inference-gateway/internal/config"
	"github.com/rshuklas/inference-gateway/internal/db"
	"github.com/rshuklas/inference-gateway/internal/gateway"
	"github.com/rshuklas/inference-gateway/internal/gateway/grpcclient"
	"github.com/rshuklas/inference-gateway/internal/gateway/middleware"
	"github.com/rshuklas/inference-gateway/internal/jobs"
	"github.com/rshuklas/inference-gateway/internal/messaging/natsclient"
	"github.com/rshuklas/inference-gateway/internal/observability/metrics"
	"github.com/rshuklas/inference-gateway/internal/observability/tracing"
	"github.com/rshuklas/inference-gateway/internal/ratelimit"
	igredis "github.com/rshuklas/inference-gateway/internal/redis"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	cfg := config.LoadGatewayConfig()
	if cfg.InternalAuthToken == "" {
		log.Fatal("[gateway] INTERNAL_AUTH_TOKEN must be set")
	}

	log.Println("[gateway] starting...")
	log.Printf("[gateway] connecting to orchestrator at %s", cfg.OrchestratorGRPCAddr)
	log.Printf("[gateway] idempotency ttl: %s", cfg.IdempotencyTTL)

	traceShutdown, err := tracing.Init(context.Background(), cfg.OTLPEndpoint, "gateway", "0.6.0")
	if err != nil {
		log.Printf("[gateway] tracing init failed (continuing without): %v", err)
		traceShutdown = func(context.Context) error { return nil }
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = traceShutdown(ctx)
	}()

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

	// Redis for idempotency replay.
	rdsCtx, rdsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rdsCancel()

	rdsClient, err := igredis.Connect(rdsCtx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("[gateway] redis connect failed: %v", err)
	}
	defer rdsClient.Close()
	log.Println("[gateway] redis connected")

	tenantRepo := tenant.NewCachedRepository(tenant.NewRepository(pool), rdsClient, cfg.AuthCacheTTL)
	rateLimitRepo := ratelimit.NewCachedRepository(ratelimit.NewRepository(pool), rdsClient, cfg.ConfigCacheTTL)

	// NATS for async job queue. If the broker is down at startup we still
	// boot - the sync path stays healthy and the async endpoint reports 503.
	natsConn, err := natsclient.Connect(cfg.NATSURL)
	if err != nil {
		log.Printf("[gateway] nats connect failed (async disabled): %v", err)
	} else {
		defer natsConn.Close()
		log.Printf("[gateway] nats connected to %s", cfg.NATSURL)
	}

	var jobsSvc gateway.JobsService
	if natsConn != nil {
		if js, err := initJetStream(natsConn); err != nil {
			log.Printf("[gateway] jetstream setup failed (async disabled): %v", err)
		} else {
			jobsSvc = jobs.NewService(jobs.NewRepository(pool), jobs.NewJetStreamPublisher(js))
		}
	}

	// gRPC client to orchestrator with tenant-propagating + tracing
	// interceptors. otelgrpc injects traceparent headers; the tenant
	// interceptor adds x-tenant-id.
	conn, err := grpc.NewClient(
		cfg.OrchestratorGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithChainUnaryInterceptor(
			grpcclient.InternalAuthUnaryInterceptor(cfg.InternalAuthToken),
			grpcclient.TenantUnaryInterceptor(),
		),
	)
	if err != nil {
		log.Fatalf("[gateway] failed to connect to orchestrator: %v", err)
	}
	defer conn.Close()

	client := pb.NewInferenceServiceClient(conn)
	handler := gateway.NewHandler(client, jobsSvc).WithRequestTimeout(cfg.RequestTimeout)

	mtr := metrics.New()

	authMW := middleware.Auth(tenantRepo)
	rateLimitMW := middleware.RateLimit(rateLimitRepo, ratelimit.New(rdsClient))
	idempotencyMW := middleware.Idempotency(rdsClient, cfg.IdempotencyTTL)
	metricsMW := middleware.Metrics(mtr, nil)
	bodyMW := middleware.MaxBody(cfg.MaxRequestBytes)

	mux := http.NewServeMux()
	// Health and /metrics are intentionally unauthenticated.
	mux.Handle("/health", metricsMW(http.HandlerFunc(handler.HandleHealth)))
	mux.Handle("/metrics", mtr.Handler())
	// Order matters: metrics wraps everything, auth attaches the tenant to
	// ctx, idempotency reads it.
	mux.Handle(
		"/v1/inference/execute",
		metricsMW(authMW(rateLimitMW(bodyMW(idempotencyMW(http.HandlerFunc(handler.HandleExecute)))))),
	)
	mux.Handle(
		"/v1/inference/execute_async",
		metricsMW(authMW(rateLimitMW(bodyMW(http.HandlerFunc(handler.HandleSubmitAsync))))),
	)
	mux.Handle(
		"/v1/jobs/",
		metricsMW(authMW(rateLimitMW(http.HandlerFunc(handler.HandleJobStatus)))),
	)

	addr := fmt.Sprintf(":%s", cfg.HTTPPort)
	tracedHandler := otelhttp.NewHandler(mux, "gateway", otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents))
	server := &http.Server{
		Addr:              addr,
		Handler:           tracedHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Must outlast the slowest inference call.
		WriteTimeout: cfg.RequestTimeout + 10*time.Second,
		IdleTimeout:  120 * time.Second,
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("[gateway] shutting down, draining in-flight requests...")
		// Give in-flight inference calls up to their full deadline to finish.
		ctx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout+5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("[gateway] shutdown incomplete: %v", err)
		}
	}()

	log.Printf("[gateway] listening on %s", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("[gateway] server error: %v", err)
	}
	<-shutdownDone

	log.Println("[gateway] stopped")
}

// initJetStream returns a JetStream context with the jobs stream in place.
func initJetStream(nc *nats.Conn) (jetstream.JetStream, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := natsclient.EnsureStream(ctx, js); err != nil {
		return nil, err
	}
	return js, nil
}
