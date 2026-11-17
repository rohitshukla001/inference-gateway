// Package config provides configuration loading for all services.
package config

import (
	"log"
	"os"
	"strconv"
	"time"
)

// GatewayConfig holds configuration for the API gateway service.
type GatewayConfig struct {
	HTTPPort             string
	OrchestratorGRPCAddr string
	PostgresURL          string
	RedisURL             string
	NATSURL              string
	OTLPEndpoint         string
	IdempotencyTTL       time.Duration
	InternalAuthToken    string
	// RequestTimeout bounds a sync inference call end to end.
	RequestTimeout time.Duration
	// MaxRequestBytes caps request bodies (and so prompt size).
	MaxRequestBytes int64
	// AuthCacheTTL is how long a resolved API key is cached; it is also the
	// longest a revoked key keeps working.
	AuthCacheTTL time.Duration
	// ConfigCacheTTL is how long per-tenant rate limits are cached.
	ConfigCacheTTL time.Duration
}

// OrchestratorConfig holds configuration for the orchestrator service.
type OrchestratorConfig struct {
	GRPCPort         string
	AdminPort        string
	OllamaEndpoint   string
	DefaultModel     string
	OllamaTimeout    time.Duration
	PostgresURL      string
	RedisURL         string
	OTLPEndpoint     string
	CacheTTL         time.Duration
	RetryMaxAttempts int
	RetryBaseBackoff time.Duration
	RetryMaxBackoff  time.Duration
	HealthInterval   time.Duration
	HealthTimeout    time.Duration
	ConfigCacheTTL   time.Duration
	// InternalAuthToken is the shared secret callers must present.
	InternalAuthToken string
	// EnableReflection registers gRPC server reflection (dev tooling only).
	EnableReflection bool
}

// WorkerConfig holds configuration for the async worker service.
type WorkerConfig struct {
	OrchestratorGRPCAddr string
	PostgresURL          string
	NATSURL              string
	OTLPEndpoint         string
	StuckJobThreshold    time.Duration
	SweepInterval        time.Duration
	JobTimeout           time.Duration
	Concurrency          int
	InternalAuthToken    string
}

// LoadGatewayConfig reads gateway configuration from environment variables with sensible defaults.
func LoadGatewayConfig() *GatewayConfig {
	return &GatewayConfig{
		HTTPPort:             getEnv("GATEWAY_HTTP_PORT", "8080"),
		OrchestratorGRPCAddr: getEnv("ORCHESTRATOR_GRPC_ADDR", "localhost:9090"),
		PostgresURL:          getEnv("POSTGRES_URL", "postgres://inference:inference@localhost:5432/inference_gateway?sslmode=disable"),
		RedisURL:             getEnv("REDIS_URL", "redis://localhost:6379/0"),
		NATSURL:              getEnv("NATS_URL", "nats://localhost:4222"),
		OTLPEndpoint:         getEnv("OTLP_ENDPOINT", "localhost:4317"),
		IdempotencyTTL:       getDuration("IDEMPOTENCY_TTL", 24*time.Hour),
		InternalAuthToken:    getEnv("INTERNAL_AUTH_TOKEN", ""),
		RequestTimeout:       getDuration("GATEWAY_REQUEST_TIMEOUT", 120*time.Second),
		MaxRequestBytes:      int64(getInt("MAX_REQUEST_BYTES", 1<<20)),
		AuthCacheTTL:         getDuration("AUTH_CACHE_TTL", time.Minute),
		ConfigCacheTTL:       getDuration("CONFIG_CACHE_TTL", 5*time.Minute),
	}
}

// LoadWorkerConfig reads worker configuration from environment variables with sensible defaults.
func LoadWorkerConfig() *WorkerConfig {
	return &WorkerConfig{
		OrchestratorGRPCAddr: getEnv("ORCHESTRATOR_GRPC_ADDR", "localhost:9090"),
		PostgresURL:          getEnv("POSTGRES_URL", "postgres://inference:inference@localhost:5432/inference_gateway?sslmode=disable"),
		NATSURL:              getEnv("NATS_URL", "nats://localhost:4222"),
		OTLPEndpoint:         getEnv("OTLP_ENDPOINT", "localhost:4317"),
		StuckJobThreshold:    getDuration("STUCK_JOB_THRESHOLD", 5*time.Minute),
		SweepInterval:        getDuration("SWEEP_INTERVAL", 30*time.Second),
		JobTimeout:           getDuration("JOB_TIMEOUT", 2*time.Minute),
		Concurrency:          getInt("WORKER_CONCURRENCY", 4),
		InternalAuthToken:    getEnv("INTERNAL_AUTH_TOKEN", ""),
	}
}

// LoadOrchestratorConfig reads orchestrator configuration from environment variables with sensible defaults.
func LoadOrchestratorConfig() *OrchestratorConfig {
	return &OrchestratorConfig{
		GRPCPort:          getEnv("ORCHESTRATOR_GRPC_PORT", "9090"),
		AdminPort:         getEnv("ORCHESTRATOR_ADMIN_PORT", "9091"),
		OllamaEndpoint:    getEnv("OLLAMA_ENDPOINT", "http://localhost:11434"),
		DefaultModel:      getEnv("OLLAMA_DEFAULT_MODEL", "llama3.2"),
		OllamaTimeout:     getDuration("OLLAMA_TIMEOUT", 120*time.Second),
		PostgresURL:       getEnv("POSTGRES_URL", "postgres://inference:inference@localhost:5432/inference_gateway?sslmode=disable"),
		RedisURL:          getEnv("REDIS_URL", "redis://localhost:6379/0"),
		OTLPEndpoint:      getEnv("OTLP_ENDPOINT", "localhost:4317"),
		CacheTTL:          getDuration("CACHE_TTL", 1*time.Hour),
		RetryMaxAttempts:  getInt("RETRY_MAX_ATTEMPTS", 3),
		RetryBaseBackoff:  getDuration("RETRY_BASE_BACKOFF", 100*time.Millisecond),
		RetryMaxBackoff:   getDuration("RETRY_MAX_BACKOFF", 2*time.Second),
		HealthInterval:    getDuration("HEALTH_INTERVAL", 15*time.Second),
		HealthTimeout:     getDuration("HEALTH_TIMEOUT", 3*time.Second),
		ConfigCacheTTL:    getDuration("CONFIG_CACHE_TTL", 5*time.Minute),
		InternalAuthToken: getEnv("INTERNAL_AUTH_TOKEN", ""),
		EnableReflection:  getEnv("GRPC_REFLECTION", "") == "true",
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

// getDuration and getInt refuse to start the service on a malformed value
// (e.g. OLLAMA_TIMEOUT=30 without a unit) rather than silently falling back
// to the default.
func getDuration(key string, fallback time.Duration) time.Duration {
	val, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		log.Fatalf("[config] invalid %s=%q: %v (want a Go duration such as 30s or 2m)", key, val, err)
	}
	return d
}

func getInt(key string, fallback int) int {
	val, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		log.Fatalf("[config] invalid %s=%q: %v (want an integer)", key, val, err)
	}
	return n
}
