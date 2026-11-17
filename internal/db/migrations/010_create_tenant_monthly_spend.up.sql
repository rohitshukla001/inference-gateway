-- Running month-to-date spend per tenant. The orchestrator reserves the
-- projected cost of a call here atomically before calling a paid provider
-- and settles the actual cost afterwards, so concurrent calls cannot
-- overshoot the monthly limit and the budget check is O(1).
CREATE TABLE tenant_monthly_spend (
    tenant_id  UUID NOT NULL REFERENCES tenants(tenant_id) ON DELETE CASCADE,
    month      DATE NOT NULL,
    spent_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, month)
);

-- Backfill from the usage log so existing spend still counts.
INSERT INTO tenant_monthly_spend (tenant_id, month, spent_usd)
SELECT tenant_id, date_trunc('month', created_at)::date, SUM(cost)
FROM usage_records
GROUP BY tenant_id, date_trunc('month', created_at)::date;
