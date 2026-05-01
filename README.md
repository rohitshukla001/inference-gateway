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

To add more tenants/keys, write SQL into a new migration file under `internal/db/migrations/`. An admin API is planned for a later phase.

## Routing and budgets

The orchestrator picks a model per request via a rule-based router. The static catalog (`internal/router/catalog.go`) lists each `(provider, model)` along with its tier, per-1k pricing, and p95 latency.

Tenants get two policy tables:

- `tenant_policies` — `allowed_models` (JSONB array) and a `default_tier` (`cheap` / `default` / `premium`). The dev tenant is seeded with all three demo models allowed and tier `default`.
- `tenant_budgets` — `monthly_limit_usd` and `soft_limit_usd`. The dev tenant has a `$10` monthly cap. Before each non-cached call, the orchestrator sums `usage_records.cost` for the current month and rejects with `402 Payment Required` if `month_to_date + projected_cost > monthly_limit`.

The router always pairs cloud-style providers with a 1-deep fallback to the local Ollama model. If the primary provider fails, the orchestrator transparently retries against Ollama (and tags the usage row with the actual served model).

## API Reference

### `POST /v1/inference/execute`

Execute an LLM inference request. **Requires `Authorization: Bearer <key>`.**

**Request:**
```json
{
  "prompt": "Explain microservices in one sentence.",
  "model": "gpt-4o-mini",
  "max_cost": 0.01,
  "latency_sla_ms": 500
}
```

All routing fields are optional:

| Field | Effect |
|---|---|
| `model` | Pin a specific model. Must be in the tenant's `allowed_models`; rejected with 403 otherwise. |
| `max_cost` | Filter the catalog to models whose worst-case projected cost fits the cap. Cheapest survivor wins. |
| `latency_sla_ms` | Filter the catalog to models whose p95 latency fits the SLA. |

If none are set, the router picks the cheapest model in the tenant's `default_tier`.

**Response:**
```json
{
  "output": "Microservices is an architectural approach...",
  "model": "gpt-4o-mini",
  "provider": "mock",
  "usage": {
    "prompt_tokens": 12,
    "completion_tokens": 45,
    "total_tokens": 57
  },
  "cached": false,
  "cost": 0.000029
}
```

`cached` is `true` when the response was served from the Redis exact-match cache (the cache key includes the chosen model). `cost` is the actual USD cost of this call computed from token counts × per-1k rates from the catalog; `0` for cache hits and for local Ollama responses.

**Optional headers:**

| Header | Effect |
|---|---|
| `Idempotency-Key: <any-string>` | Within `IDEMPOTENCY_TTL` (default 24h), repeated calls with the same key + tenant return the original response verbatim. The replay carries `Idempotent-Replay: true`. |

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
| `POSTGRES_URL` | `postgres://inference:inference@localhost:5432/inference_gateway?sslmode=disable` | Gateway, Orchestrator |
| `REDIS_URL` | `redis://localhost:6379/0` | Gateway, Orchestrator |
| `CACHE_TTL` | `1h` | Orchestrator |
| `IDEMPOTENCY_TTL` | `24h` | Gateway |

## Version

`v0.3.0` — Phase 3: Smart routing across multiple providers (Ollama + mock OpenAI-style) driven by tenant policy, request `max_cost`, and `latency_sla_ms`. Adds `tenant_policies` and `tenant_budgets` tables, a budget guard that rejects with `402 Payment Required` when month-to-date spend would exceed the cap, and a 1-deep Ollama fallback for primary failures. Response now carries `cost`.

`v0.2.0` — Phase 2: Adds Redis exact-match response cache, `Idempotency-Key` middleware, and per-request usage tracking written to Postgres (`usage_records`).

`v0.1.0` — Phase 1: Thin vertical slice with HTTP gateway, gRPC orchestrator, Ollama provider, and API-key + tenant resolution.
