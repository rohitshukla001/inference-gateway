package tenant

import (
	"context"
	"testing"
)

func TestHashAPIKey_Deterministic(t *testing.T) {
	got1 := HashAPIKey("dev-key-tenant-acme")
	got2 := HashAPIKey("dev-key-tenant-acme")
	if got1 != got2 {
		t.Fatalf("hash not deterministic: %s vs %s", got1, got2)
	}

	const wantHexLen = 64
	if len(got1) != wantHexLen {
		t.Errorf("expected hex length %d, got %d", wantHexLen, len(got1))
	}
}

func TestHashAPIKey_DifferentInputs(t *testing.T) {
	a := HashAPIKey("key-one")
	b := HashAPIKey("key-two")
	if a == b {
		t.Errorf("expected different hashes, got identical: %s", a)
	}
}

func TestContextRoundTrip(t *testing.T) {
	want := &Tenant{ID: "tid-1", Name: "ACME", Status: "active", Role: "user"}

	ctx := WithTenant(context.Background(), want)
	got := FromContext(ctx)

	if got != want {
		t.Errorf("expected same pointer back from context, got %v", got)
	}
}

func TestFromContext_Missing(t *testing.T) {
	if got := FromContext(context.Background()); got != nil {
		t.Errorf("expected nil for empty context, got %v", got)
	}
}
