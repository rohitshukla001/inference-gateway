package jobs

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rshuklas/inference-gateway/internal/db"
)

// testPool connects to the local Postgres from docker-compose (or
// POSTGRES_URL) and applies migrations. The test is skipped when no database
// is reachable so `go test ./...` still passes without infrastructure.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		url = "postgres://inference:inference@localhost:5432/inference_gateway?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Skipf("postgres not reachable, skipping: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func TestRepository_SweepStale(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	var tenantID string
	if err := pool.QueryRow(ctx, `SELECT tenant_id FROM tenants LIMIT 1`).Scan(&tenantID); err != nil {
		t.Fatalf("load tenant: %v", err)
	}

	// insert creates a job in the given state, backdated by age.
	insert := func(status string, age time.Duration) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO jobs (tenant_id, status, request_payload, created_at, started_at)
			VALUES ($1, $2, '{}', NOW() - $3::interval,
			        CASE WHEN $2 = 'pending' THEN NULL ELSE NOW() - $3::interval END)
			RETURNING job_id`, tenantID, status, age.String()).Scan(&id)
		if err != nil {
			t.Fatalf("insert %s job: %v", status, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM jobs WHERE job_id = $1`, id) })
		return id
	}

	threshold := 5 * time.Minute
	stalePending := insert("pending", 10*time.Minute)
	freshPending := insert("pending", time.Minute)
	staleRunning := insert("running", 10*time.Minute)
	freshRunning := insert("running", time.Minute)
	oldCompleted := insert("completed", 10*time.Minute)

	n, err := repo.SweepStale(ctx, threshold)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if n < 2 {
		t.Fatalf("swept %d rows, want at least 2", n)
	}

	cases := []struct {
		id         string
		wantStatus Status
		wantErr    string
	}{
		{stalePending, StatusFailed, "sweeper: stuck in pending for 5m0s"},
		{freshPending, StatusPending, ""},
		{staleRunning, StatusFailed, "sweeper: stuck in running for 5m0s"},
		{freshRunning, StatusRunning, ""},
		{oldCompleted, StatusCompleted, ""},
	}
	for _, c := range cases {
		job, err := repo.GetByID(ctx, tenantID, c.id)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", c.id, err)
		}
		if job.Status != c.wantStatus {
			t.Errorf("job %s: status = %q, want %q", c.id, job.Status, c.wantStatus)
		}
		if !strings.Contains(job.ErrorMessage, c.wantErr) {
			t.Errorf("job %s: error_message = %q, want it to contain %q", c.id, job.ErrorMessage, c.wantErr)
		}
	}

	// A worker that receives the message for a swept pending job must not
	// pick it up.
	if _, _, claimed, err := repo.Claim(ctx, stalePending); err != nil || claimed {
		t.Fatalf("Claim on swept job: claimed=%v err=%v, want claimed=false", claimed, err)
	}
}
