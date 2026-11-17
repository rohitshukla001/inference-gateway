package grpcclient

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// InternalTokenMetadataKey is the metadata key for the internal token; it
// must match the orchestrator's grpcserver constant.
const InternalTokenMetadataKey = "x-internal-token"

// InternalAuthUnaryInterceptor attaches the shared internal token to every
// outgoing call.
func InternalAuthUnaryInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		ctx = metadata.AppendToOutgoingContext(ctx, InternalTokenMetadataKey, token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
