<h1 align="center">Inference Gateway</h1>

<p align="center"><b>One gateway for every LLM call. Concurrent calls can't race past a tenant's budget, a retried request never bills twice, and a worker crash never leaves an async job stuck.</b></p>

<p align="center">
  Go 1.25 &nbsp;·&nbsp; gRPC &nbsp;·&nbsp; Postgres &nbsp;·&nbsp; Redis &nbsp;·&nbsp; NATS JetStream &nbsp;·&nbsp; OpenTelemetry &nbsp;·&nbsp; Runs locally on Ollama
</p>

## Architecture

<p align="center">
  <img src="docs/diagram/Inference%20Gateway%20%E2%80%94%20System%20Architecture.png" alt="System architecture: client to HTTP gateway, gRPC orchestrator, LLM provider, with NATS, worker, Redis, Postgres and the observability stack">
</p>

| Sync request | Async request |
| --- | --- |
| <img src="docs/diagram/Inference%20Gateway%20%E2%80%94%20Sync%20Request%20Flow.png" alt="Sync request flow from client through gateway and orchestrator to the provider and back"> | <img src="docs/diagram/Inference%20Gateway%20%E2%80%94%20Async%20Request%20Flow.png" alt="Async request flow: gateway stores a job, publishes to JetStream, worker runs it through the orchestrator"> |

A client calls the HTTP gateway with an API key. The gateway resolves the tenant, applies its rate limit and forwards the call over gRPC. The orchestrator picks a model, checks the cache and the budget, then calls the provider. Async calls go through a JetStream queue and a worker to reach the same orchestrator, so both paths get identical routing, caching and billing.

## Run it in 3 steps

You need Go 1.25+ and Docker.

1. **Start the infrastructure.** `make up` starts Postgres, Redis, NATS, Ollama, Prometheus, Grafana and Jaeger. Then `make pull-model` downloads llama3.2 (about 2 GB, first time only).
2. **Start the services,** one per terminal: `make run-orchestrator`, `make run-gateway`, and `make run-worker` for the async path. The gateway applies the database migrations on startup.
3. **Send a request** with the seeded dev key:

```bash
curl -X POST http://localhost:8080/v1/inference/execute \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer dev-key-tenant-acme" \
  -d '{"prompt": "Say hello in one sentence."}'
```

The reply carries the output, the model that served it, token usage and the cost of the call. Every endpoint is in the [API reference](docs/api.md).

## How it stays correct

Three failures are easy to ship in a gateway like this, and each one has a test that proves it can't happen here.

**Budgets hold under concurrency.** Checking spend and then calling the provider lets ten parallel requests all see the same headroom. Here one Postgres statement reserves the call's projected cost only if it still fits under the monthly limit. After the call, the reservation is settled to the real cost. Forty concurrent callers against a $5 cap get exactly five through (`internal/budget/repository_test.go`). If the budget store is down, paid calls are refused rather than let through uncapped.

**A retry never bills twice.** An `Idempotency-Key` is claimed in Redis with `SET NX` before the call runs. A duplicate that arrives while the first call is still running gets `409`. One that arrives after a success gets the stored response, and the same key with a different body gets `422` (`internal/gateway/middleware/idempotency_test.go`).

**No async job gets stuck.** A worker acknowledges a JetStream message only after writing the job's result. If it crashes first, the message goes to another worker, and a job that was already running is marked failed by a sweeper after five minutes (`internal/jobs/repository_test.go`). Either way, a client polling the job reaches an end state. On shutdown, a worker stops pulling and finishes the jobs it started (`internal/worker/worker_nats_test.go`).

## What it does

The router picks a model per request from a static catalog of three: `llama3.2` on Ollama, and `gpt-4o-mini` and `gpt-4o` on a mock provider that simulates their pricing and latency. It filters by the tenant's allowed models, the request's `max_cost` and `latency_sla_ms`, then takes the cheapest model in the tenant's preferred tier. If the chosen provider fails or is known to be down, the call falls back to local Ollama.

Around the router sit the pieces a shared gateway needs:

- an exact-match response cache in Redis.
- per-tenant requests-per-second limits.
- retries with backoff that skip errors a retry can't fix.
- a usage row with the cost of every call.
- Prometheus metrics and OpenTelemetry traces across all three services.

## Security

API keys are stored only as SHA-256 hashes. The orchestrator trusts the tenant ID it is sent, so it accepts calls only from services holding the shared `INTERNAL_AUTH_TOKEN`. Every service refuses to start without one. Request bodies are capped at 1 MiB, internal errors reach clients as generic messages, and compose binds every port to `localhost`.

## Tech stack

- Go 1.25, gRPC and Protocol Buffers
- Postgres 16 for tenants, policies, budgets, usage and jobs, with embedded migrations
- Redis 7 for the response cache, idempotency keys, rate limits and config caches
- NATS JetStream for the async job queue
- Ollama with llama3.2 as the local model
- Prometheus metrics, OpenTelemetry traces in Jaeger, Grafana dashboards

## Project structure

```
cmd/
├── gateway/                  HTTP entry point
├── orchestrator/             gRPC service
└── worker/                   Async job consumer
internal/
├── gateway/                  HTTP handlers, middleware (auth, rate limit, idempotency, body limit)
├── orchestrator/             Routing, cache, budget and fallback for each call
├── router/                   Model catalog, cost estimates, selection rules
├── budget/                   Atomic spend reservation and settlement
├── policy/  tenant/          Tenant routing policy and API-key lookup, with Redis caches
├── ratelimit/                Per-tenant fixed-window limiter
├── jobs/  worker/            Async job state, JetStream publisher, worker pool and sweeper
├── provider/                 Provider interface, Ollama, mock, health checker
├── cache/  usage/            Response cache and usage records
├── messaging/  db/  redis/   Connections and migrations
└── observability/            Prometheus metrics and OpenTelemetry tracing
proto/inference/v1/           gRPC contract between gateway, worker and orchestrator
deployments/                  Prometheus and Grafana provisioning
docs/                         API, configuration, observability, changelog and design docs
```

## Build and test

```bash
make build
go test ./internal/...
```

`make build` writes the three binaries to `bin/`. The tests that need Postgres or NATS skip themselves when those aren't running, so `make up` first for the full suite. The end-to-end tests in `test/` call a running gateway, so start the services before `go test ./test/`.

## Docs

- [API reference](docs/api.md): endpoints, headers, status codes and examples.
- [Configuration](docs/configuration.md): every environment variable and Make command.
- [Observability](docs/observability.md): metrics, dashboards and traces.
- [Changelog](docs/changelog.md): what each version added.
- Design documents: [requirements](docs/Requirement%20Document.pdf), [high-level design](docs/Inference%20Gateway%20HLD.pdf), [low-level design](docs/Inference%20Gateway%20LLD.pdf), [tech stack](docs/Tech%20Stack.pdf) and [execution plan](docs/Execution%20Plan.pdf).

## Design decisions

- The budget check refuses paid calls when Postgres is unreachable, while rate limiting allows calls when Redis is unreachable. An outage should never mean unlimited spend, and it shouldn't mean a blocked tenant either.
- A response from the fallback model is never cached. The cache key names the primary model, so the next caller would get the fallback's answer under the primary's name.
- Rate limiting uses a one-second fixed window. It costs one atomic Redis transaction per request, at the cost of allowing up to twice the limit in a burst that straddles a second boundary.
- Services authenticate to each other with a shared token rather than mTLS, which closes the trust gap without certificate management. mTLS is the next step for a real deployment.

## Disclaimer

Inference Gateway is an independent project. It is not affiliated with, endorsed by or sponsored by OpenAI, Meta or Ollama. The `gpt-4o` and `gpt-4o-mini` names label a local mock provider, and the gateway makes no calls to OpenAI.

## License

Copyright 2026 Rohit Shukla. All rights reserved.

No license is granted. You can read the code here, but you may not copy, modify or distribute it without written permission.

---

Designed and developed by Rohit Shukla
