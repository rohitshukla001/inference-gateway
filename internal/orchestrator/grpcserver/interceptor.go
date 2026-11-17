// Package grpcserver holds orchestrator-side gRPC server interceptors.
package grpcserver

import (
	"context"

	"github.com/rshuklas/inference-gateway/internal/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TenantMetadataKey is the gRPC metadata key carrying the tenant ID; it
// matches the gateway-side constant.
const TenantMetadataKey = "x-tenant-id"

// TenantUnaryInterceptor attaches a tenant.Tenant holding only the
// x-tenant-id metadata value to the context. Calls without it proceed with no
// tenant; handlers that need one reject them.
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
