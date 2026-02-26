// Package config provides configuration loading for all services.
package config

import (
	"os"
)

// GatewayConfig holds configuration for the API gateway service.
type GatewayConfig struct {
	HTTPPort             string // Port for HTTP server (default: 8080)
	OrchestratorGRPCAddr string // Address of orchestrator gRPC server (default: localhost:9090)
}

// OrchestratorConfig holds configuration for the orchestrator service.
type OrchestratorConfig struct {
	GRPCPort       string // Port for gRPC server (default: 9090)
	OllamaEndpoint string // Ollama API endpoint (default: http://localhost:11434)
	DefaultModel   string // Default model name (default: llama3.2)
}

// LoadGatewayConfig reads gateway configuration from environment variables with sensible defaults.
func LoadGatewayConfig() *GatewayConfig {
	return &GatewayConfig{
		HTTPPort:             getEnv("GATEWAY_HTTP_PORT", "8080"),
		OrchestratorGRPCAddr: getEnv("ORCHESTRATOR_GRPC_ADDR", "localhost:9090"),
	}
}

// LoadOrchestratorConfig reads orchestrator configuration from environment variables with sensible defaults.
func LoadOrchestratorConfig() *OrchestratorConfig {
	return &OrchestratorConfig{
		GRPCPort:       getEnv("ORCHESTRATOR_GRPC_PORT", "9090"),
		OllamaEndpoint: getEnv("OLLAMA_ENDPOINT", "http://localhost:11434"),
		DefaultModel:   getEnv("OLLAMA_DEFAULT_MODEL", "llama3.2"),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}
