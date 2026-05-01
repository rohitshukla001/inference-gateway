CREATE TABLE jobs (
    job_id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    status           TEXT NOT NULL DEFAULT 'pending',
    request_payload  JSONB NOT NULL,
    response_payload JSONB,
    error_message    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at       TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ,
    CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'completed', 'failed'))
);

CREATE INDEX idx_jobs_tenant_status ON jobs(tenant_id, status);
-- Helps the sweeper find orphaned 'running' rows whose worker died.
CREATE INDEX idx_jobs_running_started ON jobs(started_at) WHERE status = 'running';
