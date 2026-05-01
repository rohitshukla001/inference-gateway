CREATE TABLE tenant_rate_limits (
    tenant_id  UUID PRIMARY KEY REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    rps        INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 60 rps for the dev tenant; 0 means unlimited.
INSERT INTO tenant_rate_limits (tenant_id, rps)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    60
)
ON CONFLICT (tenant_id) DO NOTHING;
