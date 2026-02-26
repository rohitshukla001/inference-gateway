// Package orchestrator implements the gRPC InferenceService that coordinates
// between the gateway and LLM providers.
package orchestrator

import (
	"context"
	"fmt"
	"log"

	"github.com/rshuklas/inference-gateway/internal/provider"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const serviceVersion = "0.1.0"

// Service implements the gRPC InferenceService.
type Service struct {
	pb.UnimplementedInferenceServiceServer
	provider provider.Provider
}

// NewService creates a new orchestrator service with the given provider.
func NewService(p provider.Provider) *Service {
	return &Service{
		provider: p,
	}
}

// ExecuteInference handles inference requests by delegating to the configured provider.
func (s *Service) ExecuteInference(ctx context.Context, req *pb.ExecuteInferenceRequest) (*pb.ExecuteInferenceResponse, error) {
	if req.GetPrompt() == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt is required")
	}

	log.Printf("[orchestrator] executing inference | model=%s provider=%s prompt_len=%d",
		req.GetModel(), req.GetProvider(), len(req.GetPrompt()))

	providerReq := &provider.InferenceRequest{
		Prompt: req.GetPrompt(),
		Model:  req.GetModel(),
	}

	result, err := s.provider.Execute(ctx, providerReq)
	if err != nil {
		log.Printf("[orchestrator] provider error: %v", err)
		return nil, status.Error(codes.Internal, fmt.Sprintf("provider execution failed: %v", err))
	}

	log.Printf("[orchestrator] inference complete | model=%s tokens=%d", result.Model, result.TotalTokens)

	return &pb.ExecuteInferenceResponse{
		Output:   result.Output,
		Model:    result.Model,
		Provider: result.Provider,
		Usage: &pb.TokenUsage{
			PromptTokens:     result.PromptTokens,
			CompletionTokens: result.CompletionTokens,
			TotalTokens:      result.TotalTokens,
		},
	}, nil
}

// HealthCheck returns the health status of the orchestrator and its provider.
func (s *Service) HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	healthStatus := "SERVING"

	if err := s.provider.HealthCheck(ctx); err != nil {
		log.Printf("[orchestrator] provider health check failed: %v", err)
		healthStatus = "NOT_SERVING"
	}

	return &pb.HealthCheckResponse{
		Status:  healthStatus,
		Service: "orchestrator",
		Version: serviceVersion,
	}, nil
}
