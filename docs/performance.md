# Performance

On one laptop, the gateway serves about 4,000 cached requests per second at a p99 of 30 ms. Uncached, it serves about 1,400 requests per second for a single tenant. That ceiling comes from the atomic budget check, which locks the tenant's spend row twice per paid call.

## Results

Each number is the median of three 30-second k6 runs, with zero failed requests in every run.

| Path | Load | Throughput | p50 | p95 | p99 |
|---|---|---|---|---|---|
| Cache hit | 50 virtual users | 3,971 req/s | 11.8 ms | 18.6 ms | 30.4 ms |
| Full path, mock provider | 200 virtual users | 1,439 req/s | 133.7 ms | 174.8 ms | 269.3 ms |

The full path is everything a paid call does. It routes, misses the cache, reserves budget, calls the provider, settles the reservation and writes a usage row. The mock provider sleeps 80 ms per call to stand in for gpt-4o-mini, so 80 ms of every full-path latency is the simulated model. At a lighter 50 virtual users, the full path runs at 565 req/s with a p50 of 87.4 ms. That is about 7 ms of gateway, orchestrator and database work on top of the simulated model.

## Where the ceiling is

Past 200 virtual users the full path stops scaling: 400 users gave 1,235 req/s with a p99 of 580 ms. Sampling `pg_stat_activity` during that run shows why. Most active sessions wait on `Lock:transactionid` for the same row in `tenant_monthly_spend`, from the reserve `INSERT ... ON CONFLICT` and the settle `UPDATE`.

That row lock is what keeps concurrent calls from overspending, so it is the price of the guarantee and not a defect. It limits one tenant, not the gateway: each tenant has its own row. A tenant that needs more than about 1,400 paid calls per second would need its spend split across several rows, or kept in Redis with an atomic script.

The cache-hit path has a different ceiling. Above 50 virtual users its throughput stays flat while latency rises, because k6 and all three services share the same CPU.

## Test setup

- **Machine:** Apple M3 Pro, 11 cores, 36 GB, macOS 26.5. k6 ran on the same machine as the services.
- **Services:** gateway and orchestrator built with Go 1.26.5 and run natively, logging to a file, with tracing turned off.
- **Data stores:** Postgres 16.16, Redis 7.4.12 and NATS 2.10, in Docker through Rancher Desktop.
- **Tenant:** one tenant from `test/load/seed.sql`, with no rate limit and a budget too large to block.
- **Requests:** `POST /v1/inference/execute`, closed-loop (each virtual user sends its next request when the previous one returns). The cache-hit scenario repeats one prompt; the full-path scenario sends a unique prompt every time.

The results are laptop numbers. They show where the time goes and where the limit is, not what a production deployment can carry.

## Reproduce

Start the stack (`make up`, then `make run-orchestrator` and `make run-gateway`), then:

```bash
docker exec -i ig-postgres psql -U inference -d inference_gateway < test/load/seed.sql
k6 run -e SCENARIO=cached -e VUS=50 test/load/k6.js
k6 run -e SCENARIO=uncached -e VUS=200 test/load/k6.js
```
