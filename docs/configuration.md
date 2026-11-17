# Configuration

Every service reads its settings from environment variables. A malformed duration or integer stops the service at startup instead of falling back to the default.

## Environment variables

| Variable | Default | Used by |
|---|---|---|
| `INTERNAL_AUTH_TOKEN` | required | Gateway, orchestrator, worker |
| `POSTGRES_URL` | `postgres://inference:inference@localhost:5432/inference_gateway?sslmode=disable` | Gateway, orchestrator, worker |
| `REDIS_URL` | `redis://localhost:6379/0` | Gateway, orchestrator |
| `NATS_URL` | `nats://localhost:4222` | Gateway, worker |
| `OTLP_ENDPOINT` | `localhost:4317` | Gateway, orchestrator, worker |
| `CONFIG_CACHE_TTL` | `5m` | Gateway, orchestrator |
| `GATEWAY_HTTP_PORT` | `8080` | Gateway |
| `GATEWAY_REQUEST_TIMEOUT` | `120s` | Gateway |
| `MAX_REQUEST_BYTES` | `1048576` | Gateway |
| `AUTH_CACHE_TTL` | `1m` | Gateway |
| `IDEMPOTENCY_TTL` | `24h` | Gateway |
| `ORCHESTRATOR_GRPC_ADDR` | `localhost:9090` | Gateway, worker |
| `ORCHESTRATOR_GRPC_PORT` | `9090` | Orchestrator |
| `ORCHESTRATOR_ADMIN_PORT` | `9091` | Orchestrator |
| `GRPC_REFLECTION` | `false` | Orchestrator |
| `OLLAMA_ENDPOINT` | `http://localhost:11434` | Orchestrator |
| `OLLAMA_DEFAULT_MODEL` | `llama3.2` | Orchestrator |
| `OLLAMA_TIMEOUT` | `120s` | Orchestrator |
| `CACHE_TTL` | `1h` | Orchestrator |
| `RETRY_MAX_ATTEMPTS` | `3` | Orchestrator |
| `RETRY_BASE_BACKOFF` | `100ms` | Orchestrator |
| `RETRY_MAX_BACKOFF` | `2s` | Orchestrator |
| `HEALTH_INTERVAL` | `15s` | Orchestrator |
| `HEALTH_TIMEOUT` | `3s` | Orchestrator |
| `WORKER_CONCURRENCY` | `4` | Worker |
| `JOB_TIMEOUT` | `2m` | Worker |
| `SWEEP_INTERVAL` | `30s` | Worker |
| `STUCK_JOB_THRESHOLD` | `5m` | Worker |

`make run-*` sets `INTERNAL_AUTH_TOKEN` to a dev value. Set a real secret anywhere else.

`AUTH_CACHE_TTL` is also the longest a revoked API key keeps working, because resolved keys are cached for that long.

## Make commands

| Command | What it does |
|---|---|
| `make up` | Starts the infrastructure containers. |
| `make down` | Stops them. |
| `make pull-model` | Downloads llama3.2 into Ollama. |
| `make run-gateway` | Runs the gateway on `:8080`. |
| `make run-orchestrator` | Runs the orchestrator on `:9090`, with the admin server on `:9091`. |
| `make run-worker` | Runs the async worker. |
| `make build` | Builds all three binaries into `bin/`. |
| `make test` | Runs every test, including the end-to-end tests in `test/`, which need the services running. |
| `make test-e2e` | Sends a live health check and inference call to a running gateway. |
| `make proto` | Regenerates the Go code from `proto/`. Needs `protoc` and the plugins from `make proto-install`. |

## Tenants and keys

The dev tenant, its key, policy, budget and rate limit are seeded by the migrations in `internal/db/migrations/`. To add a tenant or key, write a new migration. There is no admin API yet.
