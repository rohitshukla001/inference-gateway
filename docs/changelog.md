# Changelog

## Unreleased

Correctness and security fixes from a full code review.

- Calls to the orchestrator need the shared `INTERNAL_AUTH_TOKEN`, and gRPC reflection is off unless `GRPC_REFLECTION=true`.
- Budgets reserve each call's projected cost atomically in `tenant_monthly_spend` and settle it to the real cost, so concurrent calls can't pass the limit. Paid calls are refused while the budget store is down.
- Idempotency keys are claimed with `SET NX`: `409` while the first call runs, `422` on a different body, and the key is released when a call fails.
- Async jobs moved to a JetStream durable consumer. Workers run jobs in a bounded pool, ack only after writing the result, and finish in-flight jobs on shutdown. A failed enqueue fails the job, and the sweeper also closes stale pending jobs.
- Request bodies are capped at 1 MiB, the gateway has server timeouts and a configurable request timeout, and it drains requests on shutdown.
- API-key and rate-limit lookups are cached in Redis.
- Retries skip errors a retry can't fix, and timeouts. Providers receive the 512-token output cap the budget assumes.
- `/health` returns `503` when the orchestrator isn't serving, non-UUID job IDs get `404`, and failed async jobs store client-safe errors.
- A malformed environment variable stops the service at startup, and compose binds every port to `localhost`.

## v0.6.0: production hardening

Configurable Ollama timeout, retries with exponential backoff and full jitter (3 attempts by default), and a background health checker that lets the orchestrator skip a provider it knows is down. Its state is on the admin port at `/health/providers`. Tenant policies and budget limits are cached in Redis, and each tenant gets a requests-per-second limit that returns `429` with `Retry-After`.

## v0.5.0: observability

Prometheus metrics on the gateway and orchestrator, Prometheus, Grafana and Jaeger in compose with a provisioned dashboard, and OpenTelemetry tracing across the gateway, orchestrator, worker, cache lookups and provider calls.

## v0.4.0: async pipeline

The `jobs` table and its states (`pending`, `running`, `completed`, `failed`), the `execute_async` and job status endpoints, and a worker that runs jobs through the orchestrator's `ExecuteInference`, so async calls get the same routing, cache, budget and usage tracking as sync ones. A sweeper fails jobs that get stuck.

## v0.3.0: routing and budgets

Routing across Ollama and a mock OpenAI-style provider by tenant policy, `max_cost` and `latency_sla_ms`. Tenant policy and budget tables, `402` when a call would pass the monthly limit, a fallback to Ollama when the primary fails, and `cost` in every response.

## v0.2.0: cache and idempotency

An exact-match response cache in Redis, the `Idempotency-Key` header, and a usage row per call in Postgres.

## v0.1.0: first slice

The HTTP gateway, the gRPC orchestrator, the Ollama provider, and API-key auth with tenant resolution.
