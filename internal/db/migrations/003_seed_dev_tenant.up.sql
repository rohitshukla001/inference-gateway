INSERT INTO tenants (tenant_id, name, status)
VALUES ('00000000-0000-0000-0000-000000000001', 'Dev Tenant - ACME', 'active')
ON CONFLICT (tenant_id) DO NOTHING;

-- API key plaintext: dev-key-tenant-acme
INSERT INTO api_keys (key_hash, tenant_id, role)
VALUES (
    encode(sha256('dev-key-tenant-acme'::bytea), 'hex'),
    '00000000-0000-0000-0000-000000000001',
    'user'
)
ON CONFLICT (key_hash) DO NOTHING;
