CREATE TABLE tenant_policies (
    tenant_id       UUID PRIMARY KEY REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    allowed_models  JSONB NOT NULL DEFAULT '[]'::JSONB,
    default_tier    TEXT NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Empty allowed_models is interpreted by app code as "any model in catalog".
INSERT INTO tenant_policies (tenant_id, allowed_models, default_tier)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    '["llama3.2", "gpt-4o-mini", "gpt-4o"]'::JSONB,
    'default'
)
ON CONFLICT (tenant_id) DO NOTHING;
