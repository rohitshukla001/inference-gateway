package grpcserver

import (
	"context"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestTenantUnaryInterceptor_AttachesTenantFromMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(TenantMetadataKey, "tid-7"))

	var seen *tenant.Tenant
	handler := func(ctx context.Context, _ any) (any, error) {
		seen = tenant.FromContext(ctx)
		return nil, nil
	}

	_, err := TenantUnaryInterceptor()(ctx, nil, &grpc.UnaryServerInfo{}, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if seen == nil || seen.ID != "tid-7" {
		t.Errorf("expected tenant tid-7, got %#v", seen)
	}
}

func TestTenantUnaryInterceptor_NoMetadata_NoTenant(t *testing.T) {
	var seen *tenant.Tenant
	handler := func(ctx context.Context, _ any) (any, error) {
		seen = tenant.FromContext(ctx)
		return nil, nil
	}

	_, err := TenantUnaryInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if seen != nil {
		t.Errorf("expected no tenant, got %#v", seen)
	}
}

func TestTenantUnaryInterceptor_EmptyHeaderValue_NoTenant(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(TenantMetadataKey, ""))

	var seen *tenant.Tenant
	handler := func(ctx context.Context, _ any) (any, error) {
		seen = tenant.FromContext(ctx)
		return nil, nil
	}

	_, err := TenantUnaryInterceptor()(ctx, nil, &grpc.UnaryServerInfo{}, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if seen != nil {
		t.Errorf("expected no tenant for empty header, got %#v", seen)
	}
}
