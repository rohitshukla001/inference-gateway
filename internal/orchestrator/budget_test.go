package orchestrator

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/budget"
	"github.com/rshuklas/inference-gateway/internal/provider"
	"github.com/rshuklas/inference-gateway/internal/router"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeBudget struct {
	limits     *budget.Limits
	limitsErr  error
	reserveErr error
	reserved   []float64
	settled    []float64
}

func (f *fakeBudget) GetLimits(context.Context, string) (*budget.Limits, error) {
	return f.limits, f.limitsErr
}

func (f *fakeBudget) Reserve(_ context.Context, tenantID string, amount, _ float64) (*budget.Reservation, error) {
	if f.reserveErr != nil {
		return nil, f.reserveErr
	}
	f.reserved = append(f.reserved, amount)
	return &budget.Reservation{TenantID: tenantID, Amount: amount, SpentUSD: amount}, nil
}

func (f *fakeBudget) Settle(ctx context.Context, _ *budget.Reservation, actual float64) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	f.settled = append(f.settled, actual)
	return nil
}

// budgetService routes to the paid mock gpt-4o-mini (default tier) with no
// fallback registered.
func budgetService(p *stubProvider, b *fakeBudget) *Service {
	return NewService(Deps{
		Providers:  map[string]provider.Provider{"mock": p},
		Router:     router.New(router.DefaultCatalog()),
		BudgetRepo: b,
	})
}

func paidResp() *provider.InferenceResponse {
	return &provider.InferenceResponse{Output: "ok", Model: "gpt-4o-mini", Provider: "mock", PromptTokens: 1000, CompletionTokens: 1000}
}

func call(t *testing.T, svc *Service) error {
	t.Helper()
	ctx := tenant.WithTenant(context.Background(), &tenant.Tenant{ID: "tid-1"})
	_, err := svc.ExecuteInference(ctx, &pb.ExecuteInferenceRequest{Prompt: "hello"})
	return err
}

func TestBudget_SuccessSettlesActualCost(t *testing.T) {
	b := &fakeBudget{limits: &budget.Limits{MonthlyLimitUSD: 10}}
	if err := call(t, budgetService(&stubProvider{name: "mock", resp: paidResp()}, b)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 0.00015 + 0.0006 // 1k in + 1k out at gpt-4o-mini rates
	if len(b.reserved) != 1 || len(b.settled) != 1 || math.Abs(b.settled[0]-want) > 1e-12 {
		t.Fatalf("reserved=%v settled=%v, want one reservation settled at %v", b.reserved, b.settled, want)
	}
}

func TestBudget_ProviderFailureReleasesReservation(t *testing.T) {
	b := &fakeBudget{limits: &budget.Limits{MonthlyLimitUSD: 10}}
	err := call(t, budgetService(&stubProvider{name: "mock", err: errors.New("boom")}, b))
	if err == nil {
		t.Fatal("expected error")
	}
	if len(b.settled) != 1 || b.settled[0] != 0 {
		t.Fatalf("settled=%v, want [0]", b.settled)
	}
}

func TestBudget_OverBudgetIsResourceExhausted(t *testing.T) {
	b := &fakeBudget{limits: &budget.Limits{MonthlyLimitUSD: 10}, reserveErr: &budget.OverBudgetError{SpentUSD: 9.99}}
	p := &stubProvider{name: "mock", resp: paidResp()}
	err := call(t, budgetService(p, b))
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("err = %v, want ResourceExhausted", err)
	}
	if p.calls != 0 {
		t.Fatalf("provider called %d times, want 0", p.calls)
	}
}

func TestBudget_StoreErrorRefusesPaidCall(t *testing.T) {
	for name, b := range map[string]*fakeBudget{
		"limits lookup": {limitsErr: errors.New("db down")},
		"reserve":       {limits: &budget.Limits{MonthlyLimitUSD: 10}, reserveErr: errors.New("db down")},
	} {
		t.Run(name, func(t *testing.T) {
			p := &stubProvider{name: "mock", resp: paidResp()}
			if err := call(t, budgetService(p, b)); status.Code(err) != codes.Unavailable {
				t.Fatalf("err = %v, want Unavailable", err)
			}
			if p.calls != 0 {
				t.Fatalf("provider called %d times, want 0", p.calls)
			}
		})
	}
}

func TestBudget_SettlesEvenIfCallerCancelled(t *testing.T) {
	b := &fakeBudget{limits: &budget.Limits{MonthlyLimitUSD: 10}}
	ctx, cancel := context.WithCancel(context.Background())
	svc := budgetService(&stubProvider{name: "mock", resp: paidResp()}, b)
	// Cancel after reservation by settling with an already-cancelled parent.
	cancel()
	svc.settleBudget(ctx, &budget.Reservation{Amount: 1}, 0.5)
	if len(b.settled) != 1 {
		t.Fatalf("settle skipped on cancelled ctx: %v", b.settled)
	}
}
