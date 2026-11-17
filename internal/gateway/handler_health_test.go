package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type healthOrch struct {
	pb.InferenceServiceClient
	resp *pb.HealthCheckResponse
	err  error
}

func (o *healthOrch) HealthCheck(context.Context, *pb.HealthCheckRequest, ...grpc.CallOption) (*pb.HealthCheckResponse, error) {
	return o.resp, o.err
}

func TestHandleHealth(t *testing.T) {
	cases := []struct {
		name       string
		orch       *healthOrch
		wantCode   int
		wantStatus string
	}{
		{"serving", &healthOrch{resp: &pb.HealthCheckResponse{Status: "SERVING"}}, http.StatusOK, "SERVING"},
		{"orchestrator not serving", &healthOrch{resp: &pb.HealthCheckResponse{Status: "NOT_SERVING"}}, http.StatusServiceUnavailable, "NOT_SERVING"},
		{"orchestrator unreachable", &healthOrch{err: errors.New("dial")}, http.StatusServiceUnavailable, "NOT_SERVING"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			NewHandler(c.orch, nil).HandleHealth(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
			var body HealthResponse
			_ = json.NewDecoder(rr.Body).Decode(&body)
			if rr.Code != c.wantCode || body.Status != c.wantStatus {
				t.Fatalf("got %d %q, want %d %q", rr.Code, body.Status, c.wantCode, c.wantStatus)
			}
		})
	}
}

func TestMapGRPCError_HidesInternalDetails(t *testing.T) {
	code, msg := MapGRPCError(status.Error(codes.Internal, "provider execution failed: ollama returned status 500: secret"))
	if code != http.StatusBadGateway || msg != "inference execution failed" {
		t.Fatalf("got %d %q", code, msg)
	}
	// The client already passed gateway auth, so an orchestrator auth
	// failure is our misconfiguration, not theirs.
	code, msg = MapGRPCError(status.Error(codes.Unauthenticated, "invalid internal token"))
	if code != http.StatusBadGateway || msg != "inference execution failed" {
		t.Fatalf("got %d %q", code, msg)
	}
	code, msg = MapGRPCError(status.Error(codes.ResourceExhausted, "monthly budget exceeded"))
	if code != http.StatusPaymentRequired || msg != "monthly budget exceeded" {
		t.Fatalf("got %d %q", code, msg)
	}
}
