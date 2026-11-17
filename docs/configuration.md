# Configuration

Each service reads its settings from environment variables. If a duration or an integer value is not valid, the service stops at startup. The service does not use the default value in that case.

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

The `make run-*` commands set `INTERNAL_AUTH_TOKEN` to a value for local development. For all other environments, set a secret value.

The gateway keeps each resolved API key in a cache for `AUTH_CACHE_TTL`. Thus, after you revoke a key, the key continues to work for a maximum of that time.

## Make commands

| Command | Result |
|---|---|
| `make up` | Starts the infrastructure containers. |
| `make down` | Stops the infrastructure containers. |
| `make pull-model` | Downloads llama3.2 into Ollama. |
| `make run-gateway` | Runs the gateway on port 8080. |
| `make run-orchestrator` | Runs the orchestrator on port 9090 and the admin server on port 9091. |
| `make run-worker` | Runs the async worker. |
| `make build` | Builds the three binaries into `bin/`. |
| `make test` | Runs all tests. This includes the end-to-end tests in `test/`, which require the services to run. |
| `make test-e2e` | Sends a health check and an inference request to a gateway that runs. |
| `make proto` | Generates the Go code from `proto/` again. This command requires `protoc` and the plugins from `make proto-install`. |

## Tenants and keys

The migrations in `internal/db/migrations/` add these items for the development tenant:

- the tenant
- its API key
- its routing policy
- its budget
- its rate limit

To add a tenant or a key, write a new migration. The gateway has no admin API.
