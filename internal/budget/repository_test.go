package budget

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rshuklas/inference-gateway/internal/db"
)

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

func TestRepository_ReserveIsAtomicUnderConcurrency(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	var tenantID string
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ('budget-test') RETURNING tenant_id`).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE tenant_id = $1`, tenantID) })

	const limit, amount, callers = 5.0, 1.0, 40
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted []*Reservation
		denied  int
	)
	for range callers {
		wg.Go(func() {
			res, err := repo.Reserve(ctx, tenantID, amount, limit)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				granted = append(granted, res)
			case errors.Is(err, ErrOverBudget):
				denied++
			default:
				t.Errorf("reserve: %v", err)
			}
		})
	}
	wg.Wait()

	if len(granted) != 5 || denied != callers-5 {
		t.Fatalf("granted=%d denied=%d, want 5 and %d", len(granted), denied, callers-5)
	}

	// Settling every reservation at half its projection leaves 2.5 spent,
	// so 2 more units fit and the third does not.
	for _, r := range granted {
		if err := repo.Settle(ctx, r, 0.5); err != nil {
			t.Fatalf("settle: %v", err)
		}
	}
	for i := range 3 {
		_, err := repo.Reserve(ctx, tenantID, amount, limit)
		if wantOK := i < 2; (err == nil) != wantOK {
			t.Fatalf("reserve #%d after settle: err=%v, want ok=%v", i, err, wantOK)
		}
	}
	var over *OverBudgetError
	if _, err := repo.Reserve(ctx, tenantID, amount, limit); !errors.As(err, &over) || over.SpentUSD != 4.5 {
		t.Fatalf("err=%v, want OverBudgetError with spent 4.5", err)
	}
}
