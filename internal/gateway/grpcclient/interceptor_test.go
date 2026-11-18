package grpcclient

import (
	"context"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestTenantUnaryInterceptor_AttachesHeader(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), &tenant.Tenant{ID: "tid-42"})

	var seenCtx context.Context
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		seenCtx = ctx
		return nil
	}

	err := TenantUnaryInterceptor()(ctx, "/test.Method", nil, nil, nil, invoker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	md, ok := metadata.FromOutgoingContext(seenCtx)
	if !ok {
		t.Fatal("no outgoing metadata attached")
	}
	got := md.Get(TenantMetadataKey)
	if len(got) != 1 || got[0] != "tid-42" {
		t.Errorf("expected x-tenant-id=tid-42, got %v", got)
	}
}

func TestTenantUnaryInterceptor_NoTenant_NoHeader(t *testing.T) {
	var seenCtx context.Context
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		seenCtx = ctx
		return nil
	}

	err := TenantUnaryInterceptor()(context.Background(), "/test.Method", nil, nil, nil, invoker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	md, _ := metadata.FromOutgoingContext(seenCtx)
	if got := md.Get(TenantMetadataKey); len(got) != 0 {
		t.Errorf("expected no tenant header, got %v", got)
	}
}
