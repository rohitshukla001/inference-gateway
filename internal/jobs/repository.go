package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository persists Job rows in Postgres.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wires a Repository to the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const insertSQL = `
INSERT INTO jobs (tenant_id, status, request_payload)
VALUES ($1, 'pending', $2)
RETURNING job_id
`

// Insert creates a new pending job and returns the generated job_id.
func (r *Repository) Insert(ctx context.Context, tenantID string, requestPayload []byte) (string, error) {
	var jobID string
	if err := r.pool.QueryRow(ctx, insertSQL, tenantID, requestPayload).Scan(&jobID); err != nil {
		return "", fmt.Errorf("insert job: %w", err)
	}
	return jobID, nil
}

const getByIDSQL = `
SELECT job_id, tenant_id, status, request_payload,
       response_payload, COALESCE(error_message, ''),
       created_at, started_at, completed_at
FROM jobs
WHERE job_id = $1 AND tenant_id = $2
`

// GetByID loads the job, scoped to tenantID. Returns ErrNotFound when the
// row is missing OR belongs to another tenant.
func (r *Repository) GetByID(ctx context.Context, tenantID, jobID string) (*Job, error) {
	var j Job
	err := r.pool.QueryRow(ctx, getByIDSQL, jobID, tenantID).Scan(
		&j.JobID,
		&j.TenantID,
		&j.Status,
		&j.RequestPayload,
		&j.ResponsePayload,
		&j.ErrorMessage,
		&j.CreatedAt,
		&j.StartedAt,
		&j.CompletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get job: %w", err)
	}
	return &j, nil
}

const claimSQL = `
UPDATE jobs
SET status = 'running', started_at = NOW()
WHERE job_id = $1 AND status = 'pending'
RETURNING tenant_id, request_payload
`

// Claim atomically transitions a pending job to running. Returns the job's
// tenant_id and request payload on success, or (nil, false, nil) if the
// job is no longer pending - meaning another worker grabbed it (or the
// message was redelivered), so the caller should silently move on.
func (r *Repository) Claim(ctx context.Context, jobID string) (tenantID string, requestPayload []byte, claimed bool, err error) {
	row := r.pool.QueryRow(ctx, claimSQL, jobID)
	if err := row.Scan(&tenantID, &requestPayload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, false, nil
		}
		return "", nil, false, fmt.Errorf("claim job: %w", err)
	}
	return tenantID, requestPayload, true, nil
}

const completeSuccessSQL = `
UPDATE jobs
SET status = 'completed', response_payload = $2, completed_at = NOW()
WHERE job_id = $1 AND status = 'running'
`

// CompleteSuccess records a successful execution. It is idempotent: replaying
// it after the row is already terminal is a no-op (rows affected = 0).
func (r *Repository) CompleteSuccess(ctx context.Context, jobID string, responsePayload []byte) error {
	if _, err := r.pool.Exec(ctx, completeSuccessSQL, jobID, responsePayload); err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	return nil
}

const failSQL = `
UPDATE jobs
SET status = 'failed', error_message = $2, completed_at = NOW()
WHERE job_id = $1 AND status IN ('pending', 'running')
`

// Fail records a failed execution. Accepts either pending or running so a
// pre-execution failure (e.g., bad payload) can be persisted directly.
func (r *Repository) Fail(ctx context.Context, jobID string, errMsg string) error {
	if _, err := r.pool.Exec(ctx, failSQL, jobID, errMsg); err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	return nil
}

const sweepStaleSQL = `
UPDATE jobs
SET status = 'failed',
    error_message = COALESCE(error_message, '') || 'sweeper: stuck in running for ' || $2,
    completed_at = NOW()
WHERE status = 'running'
  AND started_at < NOW() - $1::interval
RETURNING job_id
`

// SweepStale marks any running job whose worker has been silent for longer
// than threshold as failed. Safe to run from multiple workers because the
// UPDATE is set-based.
func (r *Repository) SweepStale(ctx context.Context, threshold time.Duration) (int, error) {
	rows, err := r.pool.Query(ctx, sweepStaleSQL, threshold.String(), threshold.String())
	if err != nil {
		return 0, fmt.Errorf("sweep stale: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	return count, rows.Err()
}
