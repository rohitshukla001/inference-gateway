// Package config provides configuration loading for all services.
package config

import (
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
	}
}

// LoadOrchestratorConfig reads orchestrator configuration from environment variables with sensible defaults.
func LoadOrchestratorConfig() *OrchestratorConfig {
	return &OrchestratorConfig{
		GRPCPort:       getEnv("ORCHESTRATOR_GRPC_PORT", "9090"),
		AdminPort:      getEnv("ORCHESTRATOR_ADMIN_PORT", "9091"),
		OllamaEndpoint: getEnv("OLLAMA_ENDPOINT", "http://localhost:11434"),
		DefaultModel:   getEnv("OLLAMA_DEFAULT_MODEL", "llama3.2"),
		OllamaTimeout:    getDuration("OLLAMA_TIMEOUT", 120*time.Second),
		PostgresURL:      getEnv("POSTGRES_URL", "postgres://inference:inference@localhost:5432/inference_gateway?sslmode=disable"),
		RedisURL:         getEnv("REDIS_URL", "redis://localhost:6379/0"),
		OTLPEndpoint:     getEnv("OTLP_ENDPOINT", "localhost:4317"),
		CacheTTL:         getDuration("CACHE_TTL", 1*time.Hour),
		RetryMaxAttempts: getInt("RETRY_MAX_ATTEMPTS", 3),
		RetryBaseBackoff: getDuration("RETRY_BASE_BACKOFF", 100*time.Millisecond),
		RetryMaxBackoff:  getDuration("RETRY_MAX_BACKOFF", 2*time.Second),
		HealthInterval:   getDuration("HEALTH_INTERVAL", 15*time.Second),
		HealthTimeout:    getDuration("HEALTH_TIMEOUT", 3*time.Second),
		ConfigCacheTTL:   getDuration("CONFIG_CACHE_TTL", 5*time.Minute),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if val, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return fallback
}

func getInt(key string, fallback int) int {
	if val, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}
	return fallback
}
