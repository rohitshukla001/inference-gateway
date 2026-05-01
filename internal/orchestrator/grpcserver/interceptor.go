// Package grpcserver holds orchestrator-side gRPC server interceptors.
package grpcserver

import (
	"context"

	"github.com/rshuklas/inference-gateway/internal/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TenantMetadataKey mirrors the gateway-side constant for the metadata key
// used to carry the tenant id across services.
const TenantMetadataKey = "x-tenant-id"

// TenantUnaryInterceptor returns a unary server interceptor that extracts the
// x-tenant-id from incoming gRPC metadata and attaches a tenant.Tenant carrying
// just the ID to the context. Downstream code uses tenant.FromContext to read
// it. Calls without the header proceed without a tenant; the handler may
// reject if it requires one.
func TenantUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(TenantMetadataKey); len(vals) > 0 && vals[0] != "" {
				ctx = tenant.WithTenant(ctx, &tenant.Tenant{ID: vals[0]})
			}
		}
		return handler(ctx, req)
	}
}
