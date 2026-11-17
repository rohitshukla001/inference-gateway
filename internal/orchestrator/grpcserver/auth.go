package grpcserver

import (
	"context"
	"crypto/subtle"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// InternalTokenMetadataKey carries the shared service token that the gateway
// and worker present to the orchestrator.
const InternalTokenMetadataKey = "x-internal-token"

// InternalAuthUnaryInterceptor rejects any call that does not carry the
// shared internal token. The orchestrator trusts x-tenant-id, so only the
// gateway and worker (which resolve tenants themselves) may call it.
func InternalAuthUnaryInterceptor(token string) grpc.UnaryServerInterceptor {
	want := []byte(token)
	return func(
		ctx context.Context,
		req any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		vals := md.Get(InternalTokenMetadataKey)
		if len(want) == 0 || len(vals) != 1 || subtle.ConstantTimeCompare([]byte(vals[0]), want) != 1 {
			return nil, status.Error(codes.Unauthenticated, "invalid internal token")
		}
		return handler(ctx, req)
	}
}
