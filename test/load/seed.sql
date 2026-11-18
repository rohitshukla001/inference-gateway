-- Load-test tenant: unlimited rps (0) so the rate limiter never answers 429,
-- and a budget large enough never to block, so every uncached call still
-- runs the real reserve-and-settle path. Safe to run more than once.
INSERT INTO tenants (tenant_id, name, status)
VALUES ('00000000-0000-0000-0000-0000000000aa', 'Load test', 'active')
ON CONFLICT (tenant_id) DO NOTHING;

-- API key plaintext: load-key-tenant-bench
INSERT INTO api_keys (key_hash, tenant_id, role)
VALUES (encode(sha256('load-key-tenant-bench'::bytea), 'hex'), '00000000-0000-0000-0000-0000000000aa', 'user')
ON CONFLICT (key_hash) DO NOTHING;

INSERT INTO tenant_policies (tenant_id, allowed_models, default_tier)
VALUES ('00000000-0000-0000-0000-0000000000aa', '["gpt-4o-mini"]'::JSONB, 'default')
ON CONFLICT (tenant_id) DO NOTHING;

INSERT INTO tenant_budgets (tenant_id, monthly_limit_usd, soft_limit_usd)
VALUES ('00000000-0000-0000-0000-0000000000aa', 1000000, 0)
ON CONFLICT (tenant_id) DO NOTHING;

INSERT INTO tenant_rate_limits (tenant_id, rps)
VALUES ('00000000-0000-0000-0000-0000000000aa', 0)
ON CONFLICT (tenant_id) DO NOTHING;
