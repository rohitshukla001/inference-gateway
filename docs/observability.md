# Observability

`make up` starts Prometheus, Grafana and Jaeger next to the data stores, already wired to the services.

| URL | What |
|---|---|
| `http://localhost:8080/metrics` | Gateway metrics |
| `http://localhost:9091/metrics` | Orchestrator metrics |
| `http://localhost:9091/health/providers` | Latest health of each provider, as JSON |
| `http://localhost:9094` | Prometheus |
| `http://localhost:3000` | Grafana, with anonymous admin access |
| `http://localhost:16686` | Jaeger |

## Dashboard

Grafana opens with the "Inference Gateway" dashboard already provisioned. It shows gateway requests per second, p95 inference latency, cache hit ratio, cost per tenant and provider failure rate.

## Metrics

| Metric | Type | Labels |
|---|---|---|
| `gateway_http_requests_total` | counter | method, route, status |
| `gateway_http_request_duration_seconds` | histogram | method, route, status |
| `inference_latency_seconds` | histogram | tenant, model, provider, status (`ok`, `cached`, `error`) |
| `cache_hits_total`, `cache_misses_total` | counter | tenant, model |
| `provider_failures_total` | counter | provider, model |
| `inference_cost_usd_total` | counter | tenant, model, provider |

Routes are templated, so job IDs show up as `/v1/jobs/{id}` and can't inflate the number of series. `tenant` is a label so per-tenant panels work. With thousands of tenants it would need hashing or dropping for the same reason.

## Traces

Traces go to Jaeger over OTLP gRPC. A sync call shows up as `gateway → orchestrator → cache.lookup → provider.execute`, and an async job as `worker → orchestrator → cache.lookup → provider.execute`.
