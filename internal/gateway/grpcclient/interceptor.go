// Package grpcclient holds gateway-side gRPC client utilities.
package grpcclient

import (
	"context"

	"github.com/rshuklas/inference-gateway/internal/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TenantMetadataKey is the gRPC metadata key used to propagate the resolved
// tenant id from the gateway to downstream services.
const TenantMetadataKey = "x-tenant-id"

// TenantUnaryInterceptor returns a unary client interceptor that attaches the
// tenant id from the request context to the outgoing gRPC metadata. If no
// tenant is present, the call proceeds without the header (the server will
// decide how to handle that).
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
