// Load test for POST /v1/inference/execute.
//
//   k6 run -e SCENARIO=cached   test/load/k6.js   same prompt: Redis cache hits
//   k6 run -e SCENARIO=uncached test/load/k6.js   unique prompts: full path
//
// The uncached path calls the mock provider, which sleeps 80 ms per call to
// simulate gpt-4o-mini, then reserves and settles budget and writes a usage
// row in Postgres. Seed the tenant first with test/load/seed.sql.
import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const SCENARIO = __ENV.SCENARIO || 'cached';
const VUS = Number(__ENV.VUS || 50);
const DURATION = __ENV.DURATION || '30s';

export const options = {
  scenarios: {
    load: { executor: 'constant-vus', vus: VUS, duration: DURATION },
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  thresholds: { http_req_failed: ['rate<0.001'] },
};

const params = {
  headers: {
    'Content-Type': 'application/json',
    Authorization: 'Bearer load-key-tenant-bench',
  },
};

export function setup() {
  // Warm the cache entry the cached scenario reads.
  http.post(`${BASE}/v1/inference/execute`, JSON.stringify({ prompt: 'load test cached prompt' }), params);
}

export default function () {
  const prompt = SCENARIO === 'cached'
    ? 'load test cached prompt'
    : `load test ${exec.vu.idInTest}-${exec.vu.iterationInScenario}-${Date.now()}`;
  const res = http.post(`${BASE}/v1/inference/execute`, JSON.stringify({ prompt }), params);
  check(res, {
    'status 200': (r) => r.status === 200,
    'cache matches scenario': (r) => r.json('cached') === (SCENARIO === 'cached'),
  });
}
