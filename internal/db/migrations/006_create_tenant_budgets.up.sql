CREATE TABLE tenant_budgets (
    tenant_id           UUID PRIMARY KEY REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    monthly_limit_usd   DOUBLE PRECISION NOT NULL DEFAULT 0,
    soft_limit_usd      DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- $10 monthly limit for the dev tenant; soft warn at $8.
INSERT INTO tenant_budgets (tenant_id, monthly_limit_usd, soft_limit_usd)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    10.0,
    8.0
)
ON CONFLICT (tenant_id) DO NOTHING;
