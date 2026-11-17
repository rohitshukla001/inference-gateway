# API reference

All endpoints except `/health` and `/metrics` require the header `Authorization: Bearer <key>`. Each key belongs to one tenant. Each endpoint below operates only on the data of that tenant.

Migration `003_seed_dev_tenant.up.sql` adds the key `dev-key-tenant-acme` for local development.

## `POST /v1/inference/execute`

This endpoint runs an inference request and returns the answer.

```json
{
  "prompt": "Explain microservices in one sentence.",
  "model": "gpt-4o-mini",
  "max_cost": 0.01,
  "latency_sla_ms": 500
}
```

Only `prompt` is necessary. The three routing fields limit the models that the router can select:

| Field | Effect |
|---|---|
| `model` | Selects one model. If the model is not in the `allowed_models` list of the tenant, the gateway returns `403`. |
| `max_cost` | Removes each model whose maximum projected cost in USD is more than this value. The router selects the least expensive model that remains. |
| `latency_sla_ms` | Removes each model whose p95 latency is more than this value. |

If the request has none of these fields, the router selects the least expensive model in the `default_tier` of the tenant.

```json
{
  "output": "Microservices is an architectural approach that splits an application into small services.",
  "model": "gpt-4o-mini",
  "provider": "mock",
  "usage": { "prompt_tokens": 12, "completion_tokens": 45, "total_tokens": 57 },
  "cached": false,
  "cost": 0.000029
}
```

`cached` is `true` when the answer comes from the Redis cache. The cache key includes the selected model.

`cost` is the cost of this request in USD. The orchestrator calculates the cost from the token counts and the per-1k rates of the model. The cost is `0` for a cache hit and for Ollama.

### Idempotency

To make a request safe to send again, add the header `Idempotency-Key: <any string>`. The key is valid for `IDEMPOTENCY_TTL`, which is 24 hours by default. During that time, the gateway uses these rules:

- If the first request succeeded, a repeat request gets the original response and the header `Idempotent-Replay: true`.
- If the first request is not complete, a repeat request gets `409` and a `Retry-After` header.
- If a repeat request has a different body, the repeat request gets `422`.

If the first request fails, the gateway releases the key. The client can then send the request again with the same key.

### Budgets

Before the orchestrator sends a request to a paid model, the orchestrator reserves the projected cost against the monthly limit of the tenant. One atomic database statement does this reservation. After the request, the orchestrator changes the reservation to the actual cost.

The projection uses a maximum of 512 output tokens. The orchestrator sends this maximum to the provider.

If a request will make the monthly spend more than the limit, the gateway returns `402`. If the budget store is not available, the gateway returns `503` for each request to a paid model.

### Rate limits

Each tenant has a limit of requests per second in the `tenant_rate_limits` table. A value of `0` means no limit. The development tenant has a limit of 60.

If a tenant sends more requests than its limit, the gateway returns `429`. The `Retry-After` header gives the wait time in seconds. If Redis or Postgres fails, the rate limiter lets all requests through.

## `POST /v1/inference/execute_async`

This endpoint accepts the same body as `/v1/inference/execute`. The gateway does these steps:

1. The gateway stores a pending job.
2. The gateway publishes the job to the `INFERENCE_JOBS` stream in JetStream.
3. The gateway returns the response after the broker stores the message.

```json
{ "job_id": "9b2f3c50-1a2b-4c3d-8e9f-0123456789ab", "status": "pending" }
```

The status code is `202`. If the publish operation fails, the gateway marks the job `failed` and returns `500`. In that case, send the request again.

## `GET /v1/jobs/{job_id}`

This endpoint returns the job and its status: `pending`, `running`, `completed` or `failed`. The gateway returns `404` in these conditions:

- The job ID is not a UUID.
- The job does not exist.
- The job belongs to a different tenant.

```json
{
  "job_id": "9b2f3c50-1a2b-4c3d-8e9f-0123456789ab",
  "status": "completed",
  "created_at": "2026-05-01T18:32:11Z",
  "started_at": "2026-05-01T18:32:12Z",
  "completed_at": "2026-05-01T18:32:14Z",
  "result": {
    "output": "Microservices is an architectural approach that splits an application into small services.",
    "model": "gpt-4o-mini",
    "provider": "mock",
    "usage": { "prompt_tokens": 12, "completion_tokens": 45, "total_tokens": 57 },
    "cached": false,
    "cost": 0.000029
  }
}
```

A failed job has an `error` field instead of a `result` field. The error message is the same message that the synchronous endpoint returns. An example is `monthly budget exceeded: spent $9.9990 of $10.00, projected $0.0004 more`.

If a worker stops during a job, the sweeper marks the job `failed` after `STUCK_JOB_THRESHOLD`. The default value is 5 minutes. Thus, a client that polls a job always gets a final status.

## `GET /health`

This endpoint shows the status of the gateway and the orchestrator. The endpoint does not require an API key.

```json
{
  "status": "SERVING",
  "gateway": "healthy",
  "orchestrator": { "status": "SERVING", "version": "0.6.0" }
}
```

If the orchestrator is not available, or the orchestrator has no healthy provider, the endpoint returns `503` and `"status": "NOT_SERVING"`.

## Status codes

| Code | Condition |
|---|---|
| `400` | The prompt is missing, the JSON is not valid, or the model is not in the catalog. |
| `401` | The API key is missing or not known. |
| `402` | The request will make the monthly spend of the tenant more than the limit. |
| `403` | The selected model is not in the `allowed_models` list of the tenant. |
| `404` | The job does not exist, or the job belongs to a different tenant. |
| `409` | A request with the same idempotency key is not complete. |
| `413` | The body is larger than `MAX_REQUEST_BYTES`, which is 1 MiB by default. |
| `422` | The idempotency key is already in use with a different body. |
| `429` | The tenant sent more requests than its rate limit. |
| `500` | The gateway cannot put an async job in the queue. |
| `502` | All providers failed, or an internal error occurred. The message does not show the internal details. |
| `503` | The budget store or the orchestrator is not available. |
