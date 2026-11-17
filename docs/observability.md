# Observability

`make up` starts Prometheus, Grafana and Jaeger next to the data stores, already wired to the services. Together they show request rates, latency, cache hits, cost per tenant and a trace for each request.

| URL | What |
|---|---|
| `http://localhost:8080/metrics` | Gateway metrics |
| `http://localhost:9091/metrics` | Orchestrator metrics |
| `http://localhost:9091/health/providers` | Latest health of each provider, as JSON |
| `http://localhost:9094` | Prometheus |
| `http://localhost:3000` | Grafana. Anonymous users get admin access, and compose binds the port to `localhost`, so only your machine can reach it. |
| `http://localhost:16686` | Jaeger |

## Dashboard

Grafana opens with the "Inference Gateway" dashboard already provisioned. It shows gateway requests per second, p95 inference latency and cache hit ratio. It also shows cost per tenant and the provider failure rate.

## Metrics

| Metric | Emitted by | Type | Labels |
|---|---|---|---|
| `gateway_http_requests_total` | gateway | counter | method, route, status |
| `gateway_http_request_duration_seconds` | gateway | histogram | method, route, status |
| `inference_latency_seconds` | orchestrator | histogram | tenant, model, provider, status (`ok`, `cached`, `error`) |
| `cache_hits_total`, `cache_misses_total` | orchestrator | counter | tenant, model |
| `provider_failures_total` | orchestrator | counter | provider, model |
| `inference_cost_usd_total` | orchestrator | counter | tenant, model, provider |

Routes are templated, so job IDs appear as `/v1/jobs/{id}` and don't create a series per job. `tenant` is a label so that per-tenant panels work, but every tenant adds its own series. With thousands of tenants, drop the label from the latency and cache metrics and read per-tenant cost from `usage_records` instead.

## Traces

Traces go to Jaeger over OTLP gRPC. A sync call appears as one trace: `gateway → orchestrator → cache.lookup → provider.execute`.

An async job appears as two separate traces, because trace context doesn't cross the JetStream hop. The gateway's trace ends when it publishes the job. The worker then starts a new one: `worker → orchestrator → cache.lookup → provider.execute`.
