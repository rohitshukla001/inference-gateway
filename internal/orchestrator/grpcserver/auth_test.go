package grpcserver

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestInternalAuthUnaryInterceptor(t *testing.T) {
	cases := []struct {
		name       string
		serverTok  string
		md         metadata.MD
		wantCalled bool
	}{
		{"valid token", "secret", metadata.Pairs(InternalTokenMetadataKey, "secret"), true},
		{"wrong token", "secret", metadata.Pairs(InternalTokenMetadataKey, "nope"), false},
		{"missing token", "secret", metadata.MD{}, false},
		{"no metadata", "secret", nil, false},
		{"duplicate tokens", "secret", metadata.Pairs(InternalTokenMetadataKey, "secret", InternalTokenMetadataKey, "secret"), false},
		{"empty server token fails closed", "", metadata.Pairs(InternalTokenMetadataKey, ""), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			if c.md != nil {
				ctx = metadata.NewIncomingContext(ctx, c.md)
			}
			called := false
			handler := func(context.Context, any) (any, error) { called = true; return nil, nil }

			_, err := InternalAuthUnaryInterceptor(c.serverTok)(ctx, nil, &grpc.UnaryServerInfo{}, handler)
			if called != c.wantCalled {
				t.Fatalf("handler called = %v, want %v", called, c.wantCalled)
			}
			if !c.wantCalled && status.Code(err) != codes.Unauthenticated {
				t.Fatalf("err = %v, want Unauthenticated", err)
			}
		})
	}
}
