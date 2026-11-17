# Changelog

## Unreleased

Closes the gaps that let a caller bypass auth, overspend a budget, bill a retry twice or leave an async job stuck.

### Action needed when you upgrade

- Set `INTERNAL_AUTH_TOKEN` on the gateway, the orchestrator and the worker. Each service refuses to start without it.
- Run NATS with JetStream enabled (`-js`). The compose file already does.
- Check your environment variables. A malformed duration or integer now stops the service at startup.
- Expect migrations 009 and 010 to run when the gateway starts. Migration 010 backfills month-to-date spend from `usage_records`.

### Security

- The orchestrator accepts calls only from services holding `INTERNAL_AUTH_TOKEN`.
- gRPC reflection is off unless `GRPC_REFLECTION=true`.
- Request bodies are capped at 1 MiB.
- Compose binds every port to `localhost`.
- A failed async job stores the same generic message the sync API returns, without provider responses or internal error details.

### Budgets and billing

- Each paid call reserves its projected cost in `tenant_monthly_spend` with one atomic statement, then settles to the real cost. Concurrent calls can no longer pass the monthly limit together.
- Paid calls get `503` while the budget store is down, instead of going through uncapped.
- Idempotency keys are claimed with `SET NX`. A repeat gets `409` while the first call runs, and `422` if its body differs.
- A failed call releases its idempotency key, so the client can retry with the same key.
- Providers receive the 512-token output cap that the budget projection assumes.

### Async jobs

- Jobs moved from a core NATS queue subscription, which dropped messages when no worker was listening, to a JetStream durable consumer.
- Workers run up to `WORKER_CONCURRENCY` jobs at once and ack a message only after writing its result.
- Workers finish in-flight jobs on shutdown.
- A job whose enqueue fails is marked `failed`, and the gateway returns `500`.
- The sweeper also closes pending jobs that no worker picked up.

### Gateway and orchestrator

- Retries skip errors a retry can't fix, such as a provider `404`.
- Timeouts aren't retried either. A timed-out attempt has used its whole time budget, so the call moves straight to the fallback.
- API-key and rate-limit lookups are cached in Redis.
- The gateway has server timeouts and a configurable request timeout (`GATEWAY_REQUEST_TIMEOUT`), and drains in-flight requests on shutdown.
- `/health` returns `503` when the orchestrator isn't serving.
- A job ID that isn't a UUID gets `404` instead of `500`.

## v0.6.0: production hardening

- The Ollama timeout is configurable (`OLLAMA_TIMEOUT`).
- Provider calls retry with exponential backoff and full jitter, 3 attempts by default.
- A background health checker tracks each provider, and the orchestrator skips a provider the checker reports as down.
- The health checker's state is on the orchestrator's admin port at `/health/providers`.
- Tenant policies and budget limits are cached in Redis.
- Each tenant has a requests-per-second limit that returns `429` with `Retry-After`.

## v0.5.0: observability

- The gateway and the orchestrator expose `/metrics` endpoints.
- Compose runs Prometheus, Grafana and Jaeger, with a provisioned dashboard.
- OpenTelemetry tracing covers the gateway, the orchestrator, the worker, cache lookups and provider calls.

## v0.4.0: async pipeline

- A `jobs` table tracks four states: `pending`, `running`, `completed` and `failed`.
- `POST /v1/inference/execute_async` submits a job, and `GET /v1/jobs/{job_id}` returns it.
- A worker reads jobs from a core NATS queue subscription and runs each one through the orchestrator's `ExecuteInference`. Async calls get the same routing, cache, budget and usage tracking as sync ones.
- A sweeper fails jobs stuck in `running`.

## v0.3.0: routing and budgets

- The router chooses between Ollama and a mock OpenAI-style provider by tenant policy, `max_cost` and `latency_sla_ms`.
- Tenant policy and budget tables set each tenant's allowed models and monthly limit. A call that would pass the limit gets `402`.
- The call falls back to Ollama when the primary provider fails.
- Every response carries `cost`.

## v0.2.0: cache and idempotency

- An exact-match response cache in Redis.
- The `Idempotency-Key` header.
- A usage row in Postgres for every call.

## v0.1.0: first slice

- The HTTP gateway, the gRPC orchestrator and the Ollama provider.
- API-key auth with tenant resolution.
