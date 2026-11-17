# API reference

Every endpoint except `/health` and `/metrics` needs `Authorization: Bearer <key>`. Each key belongs to one tenant, and everything below is scoped to that tenant. The local dev key `dev-key-tenant-acme` is seeded by migration `003_seed_dev_tenant.up.sql`.

## `POST /v1/inference/execute`

Runs an inference call and waits for the answer.

```json
{
  "prompt": "Explain microservices in one sentence.",
  "model": "gpt-4o-mini",
  "max_cost": 0.01,
  "latency_sla_ms": 500
}
```

Only `prompt` is required. The routing fields narrow the choice of model:

| Field | Effect |
|---|---|
| `model` | Pins a model. It must be in the tenant's `allowed_models`, or the call gets `403`. |
| `max_cost` | Keeps only models whose worst-case projected cost (USD) fits the cap. The cheapest survivor wins. |
| `latency_sla_ms` | Keeps only models whose p95 latency fits the limit. |

With none set, the router picks the cheapest model in the tenant's `default_tier`.

```json
{
  "output": "Microservices is an architectural approach...",
  "model": "gpt-4o-mini",
  "provider": "mock",
  "usage": { "prompt_tokens": 12, "completion_tokens": 45, "total_tokens": 57 },
  "cached": false,
  "cost": 0.000029
}
```

`cached` is `true` when the answer came from the Redis cache, whose key includes the chosen model. `cost` is the USD cost of this call from token counts and the model's per-1k rates. It is `0` for cache hits and for Ollama.

### Idempotency

Send `Idempotency-Key: <any string>` to make a call safe to retry. Within `IDEMPOTENCY_TTL` (24 h by default):

- a repeat after the first call succeeded returns the original response, with `Idempotent-Replay: true`.
- a repeat while the first call is still running gets `409` and `Retry-After`.
- the same key with a different body gets `422`.

A failed call releases its key, so the client can retry with it.

### Budgets

Before calling a paid model, the orchestrator reserves the call's projected cost against the tenant's monthly limit in one atomic statement, and settles it to the real cost afterwards. The projection assumes at most 512 output tokens, and that cap is sent to the provider. A call that would pass the limit gets `402`. If the budget store is unreachable, paid calls get `503`.

### Rate limits

Each tenant has a requests-per-second cap in `tenant_rate_limits` (`0` means unlimited; the dev tenant has 60). Calls over it get `429` with `Retry-After` in seconds. If Redis or Postgres fails, the limiter lets calls through.

## `POST /v1/inference/execute_async`

Takes the same body as `/v1/inference/execute`, stores a pending job, publishes it to the `INFERENCE_JOBS` JetStream stream, and returns once the broker has stored the message.

```json
{ "job_id": "9b2f3c50-1a2b-4c3d-8e9f-0123456789ab", "status": "pending" }
```

The response is `202`. If publishing fails, the job is marked `failed` and the call gets `500`, so submit it again.

## `GET /v1/jobs/{job_id}`

Returns the job's state: `pending`, `running`, `completed` or `failed`. A job ID that isn't a UUID, or belongs to another tenant, gets `404`.

```json
{
  "job_id": "9b2f3c50-1a2b-4c3d-8e9f-0123456789ab",
  "status": "completed",
  "created_at": "2026-05-01T18:32:11Z",
  "started_at": "2026-05-01T18:32:12Z",
  "completed_at": "2026-05-01T18:32:14Z",
  "result": {
    "output": "...",
    "model": "gpt-4o-mini",
    "provider": "mock",
    "usage": { "prompt_tokens": 12, "completion_tokens": 45, "total_tokens": 57 },
    "cached": false,
    "cost": 0.000029
  }
}
```

A failed job carries `error` instead of `result`, with the same client-safe message the sync endpoint would have returned, for example `monthly budget exceeded: spent $9.9990 of $10.00, projected $0.0004 more`.

A job whose worker died mid-run is marked `failed` by the sweeper after `STUCK_JOB_THRESHOLD` (5 minutes by default), so polling always reaches an end state.

## `GET /health`

Reports the gateway and orchestrator status. No API key needed.

```json
{
  "status": "SERVING",
  "gateway": "healthy",
  "orchestrator": { "status": "SERVING", "version": "0.6.0" }
}
```

It returns `503` with `"status": "NOT_SERVING"` when the orchestrator is unreachable or has no healthy provider.

## Status codes

| Code | When |
|---|---|
| `400` | Missing prompt, malformed JSON, or a model that isn't in the catalog. |
| `401` | Missing or unknown API key. |
| `402` | The call would pass the tenant's monthly budget. |
| `403` | The pinned model isn't in the tenant's `allowed_models`. |
| `404` | Unknown job, or one that belongs to another tenant. |
| `409` | The idempotency key belongs to a call that is still running. |
| `413` | The body is larger than `MAX_REQUEST_BYTES` (1 MiB by default). |
| `422` | The idempotency key was used with a different body. |
| `429` | Over the tenant's rate limit. |
| `500` | An async job couldn't be enqueued. |
| `502` | Every provider failed, or an internal error. The message is generic. |
| `503` | The budget store or the orchestrator is unavailable. |
