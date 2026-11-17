// Package grpcclient holds gRPC client interceptors used by the gateway and worker.
package grpcclient

import (
	"context"

	"github.com/rshuklas/inference-gateway/internal/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TenantMetadataKey is the gRPC metadata key that carries the tenant id.
const TenantMetadataKey = "x-tenant-id"

// TenantUnaryInterceptor copies the tenant id from ctx into outgoing
// metadata. Calls without a tenant go out without it; the server decides.
func TenantUnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if t := tenant.FromContext(ctx); t != nil {
			ctx = metadata.AppendToOutgoingContext(ctx, TenantMetadataKey, t.ID)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
