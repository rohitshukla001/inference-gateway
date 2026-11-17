package grpcclient

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// InternalTokenMetadataKey mirrors the orchestrator-side constant.
const InternalTokenMetadataKey = "x-internal-token"

// InternalAuthUnaryInterceptor attaches the shared internal token to every
// outgoing call so the orchestrator accepts it.
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
