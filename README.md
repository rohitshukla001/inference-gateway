# Inference Gateway

A centralized Go microservice that acts as a unified gateway for LLM inference calls. Routes requests through a clean service boundary: **HTTP Gateway → gRPC Orchestrator → LLM Provider**.

## Architecture

```
┌──────────┐       HTTP        ┌──────────────┐       gRPC       ┌──────────────┐
│  Client   │ ───────────────▶ │   Gateway    │ ──────────────▶  │ Orchestrator │
│ (curl)    │ ◀─────────────── │  (:8080)     │ ◀──────────────  │   (:9090)    │
└──────────┘       JSON        └──────────────┘                  └──────┬───────┘
                                                                        │
                                                                        │ HTTP
                                                                        ▼
                                                                  ┌──────────┐
                                                                  │  Ollama  │
                                                                  │ (:11434) │
                                                                  └──────────┘
```

## Quick Start (3 Steps)

### Prerequisites

- Go 1.22+
- Docker & Docker Compose
- protoc + Go plugins (`make proto-install`)

### 1. Start Infrastructure

```bash
make up
make pull-model   # Downloads llama3.2 (~2GB, first time only)
```

### 2. Generate Proto & Start Services

```bash
make proto

# Terminal 1
make run-orchestrator

# Terminal 2
make run-gateway
```

### 3. Test It

```bash
# Health check (no auth)
curl http://localhost:8080/health

# Execute inference (requires Bearer API key)
curl -X POST http://localhost:8080/v1/inference/execute \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer dev-key-tenant-acme" \
  -d '{"prompt": "Say hello in one sentence."}'
```

The `dev-key-tenant-acme` key is seeded automatically by migration `003_seed_dev_tenant.up.sql` for local use.

## Authentication

The gateway uses **API key auth** via `Authorization: Bearer <key>`. Keys are stored as `sha256(key)` in Postgres, never in plaintext. Each key maps to one tenant; the resolved tenant id is propagated to the orchestrator via the gRPC metadata header `x-tenant-id`.

To add more tenants/keys, write SQL into a new migration file under `internal/db/migrations/`. An admin API is planned for Phase 2.

## API Reference

### `POST /v1/inference/execute`

Execute an LLM inference request. **Requires `Authorization: Bearer <key>`.**

**Request:**
```json
{
  "prompt": "Explain microservices in one sentence.",
  "model": "llama3.2",
  "provider": "ollama"
}
```

**Response:**
```json
{
  "output": "Microservices is an architectural approach...",
  "model": "llama3.2",
  "provider": "ollama",
  "usage": {
    "prompt_tokens": 12,
    "completion_tokens": 45,
    "total_tokens": 57
  }
}
```

### `GET /health`

Health check for gateway and orchestrator.

**Response:**
```json
{
  "status": "SERVING",
  "gateway": "healthy",
  "orchestrator": {
    "status": "SERVING",
    "version": "0.1.0"
  }
}
```

## Project Structure

```
inference-gateway/
├── cmd/
│   ├── gateway/          # HTTP entry point
│   └── orchestrator/     # gRPC service
├── internal/
│   ├── config/           # Environment-based configuration
│   ├── gateway/          # HTTP handlers + REST → gRPC translation
│   ├── orchestrator/     # gRPC service implementation
│   └── provider/         # Provider abstraction
│       └── ollama/       # Ollama provider implementation
├── proto/
│   └── inference/v1/     # Protobuf service definition
├── docker-compose.yml    # Local infrastructure (Ollama, Postgres, Redis)
├── Makefile              # Developer commands
└── README.md
```

## Make Commands

| Command | Description |
|---------|-------------|
| `make run-gateway` | Start gateway HTTP server (:8080) |
| `make run-orchestrator` | Start orchestrator gRPC server (:9090) |
| `make proto` | Generate Go code from proto definitions |
| `make up` | Start infrastructure containers |
| `make down` | Stop infrastructure containers |
| `make build` | Build both service binaries |
| `make test-e2e` | Run end-to-end smoke test |
| `make pull-model` | Pull llama3.2 into Ollama |

## Configuration

Environment variables with defaults:

| Variable | Default | Service |
|----------|---------|---------|
| `GATEWAY_HTTP_PORT` | `8080` | Gateway |
| `ORCHESTRATOR_GRPC_ADDR` | `localhost:9090` | Gateway |
| `ORCHESTRATOR_GRPC_PORT` | `9090` | Orchestrator |
| `OLLAMA_ENDPOINT` | `http://localhost:11434` | Orchestrator |
| `OLLAMA_DEFAULT_MODEL` | `llama3.2` | Orchestrator |
| `POSTGRES_URL` | `postgres://inference:inference@localhost:5432/inference_gateway?sslmode=disable` | Gateway |

## Version

`v0.1.0` — Phase 1: Thin vertical slice with HTTP gateway, gRPC orchestrator, and Ollama provider.
