// Package orchestrator implements the gRPC InferenceService that coordinates
// between the gateway and LLM providers.
package orchestrator

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/rshuklas/inference-gateway/internal/cache/responsecache"
	"github.com/rshuklas/inference-gateway/internal/provider"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	"github.com/rshuklas/inference-gateway/internal/usage"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const serviceVersion = "0.2.0"

// Service implements the gRPC InferenceService.
type Service struct {
	pb.UnimplementedInferenceServiceServer

	provider  provider.Provider
	cache     *responsecache.Cache
	usageRepo *usage.Repository
}

// NewService wires the orchestrator with its provider, cache, and usage repo.
// All three are required; pass nils only when constructing for tests that
// stub the surrounding flow.
func NewService(p provider.Provider, c *responsecache.Cache, u *usage.Repository) *Service {
	return &Service{
		provider:  p,
		cache:     c,
		usageRepo: u,
	}
}

// cachedEntry is the on-wire shape of a cached response. Mirrors the fields we
// hand back to clients but lives in this package so the cache layer stays
// agnostic of provider/proto types.
type cachedEntry struct {
	Output           string `json:"output"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	PromptTokens     int32  `json:"prompt_tokens"`
	CompletionTokens int32  `json:"completion_tokens"`
	TotalTokens      int32  `json:"total_tokens"`
}

// ExecuteInference handles inference requests with cache-aside read-through.
// On a hit, the provider is skipped entirely. Every call records a usage row
// (best-effort: a usage write failure does not fail the request).
func (s *Service) ExecuteInference(ctx context.Context, req *pb.ExecuteInferenceRequest) (*pb.ExecuteInferenceResponse, error) {
	if req.GetPrompt() == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt is required")
	}

	t := tenant.FromContext(ctx)
	if t == nil {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	start := time.Now()

	model := req.GetModel()
	if model == "" {
		// We don't know the provider's default until after Execute, but for
		// caching we need a stable key. Empty model is normalised to "default"
		// so two calls with no explicit model still share a cache slot.
		model = "default"
	}

	cacheKey := responsecache.Key(t.ID, model, req.GetPrompt())

	// Cache lookup
	if s.cache != nil {
		var entry cachedEntry
		hit, err := s.cache.Get(ctx, cacheKey, &entry)
		if err != nil {
			// Cache failure is non-fatal: log and fall through to provider.
			log.Printf("[orchestrator] cache get failed: %v", err)
		} else if hit {
			latency := time.Since(start)
			log.Printf("[orchestrator] cache HIT | tenant=%s model=%s latency_ms=%d", t.ID, model, latency.Milliseconds())

			s.recordUsage(ctx, usage.Record{
				TenantID:     t.ID,
				Model:        entry.Model,
				Provider:     entry.Provider,
				InputTokens:  entry.PromptTokens,
				OutputTokens: entry.CompletionTokens,
				LatencyMs:    latency.Milliseconds(),
				Cost:         0,
				Cached:       true,
			})

			return &pb.ExecuteInferenceResponse{
				Output:   entry.Output,
				Model:    entry.Model,
				Provider: entry.Provider,
				Usage: &pb.TokenUsage{
					PromptTokens:     entry.PromptTokens,
					CompletionTokens: entry.CompletionTokens,
					TotalTokens:      entry.TotalTokens,
				},
				Cached: true,
			}, nil
		}
	}

	// Cache miss: call provider.
	log.Printf("[orchestrator] cache MISS | tenant=%s model=%s", t.ID, model)

	providerReq := &provider.InferenceRequest{
		Prompt: req.GetPrompt(),
		Model:  req.GetModel(),
	}

	result, err := s.provider.Execute(ctx, providerReq)
	if err != nil {
		log.Printf("[orchestrator] provider error: %v", err)
		return nil, status.Error(codes.Internal, fmt.Sprintf("provider execution failed: %v", err))
	}

	latency := time.Since(start)

	// Best-effort cache write keyed on the resolved model so subsequent calls
	// with the same explicit model name still hit.
	if s.cache != nil {
		entry := cachedEntry{
			Output:           result.Output,
			Model:            result.Model,
			Provider:         result.Provider,
			PromptTokens:     result.PromptTokens,
			CompletionTokens: result.CompletionTokens,
			TotalTokens:      result.TotalTokens,
		}
		// Cache under the original requested model key so the next call with
		// the same input lands a hit, not a second cold compute.
		if err := s.cache.Set(ctx, cacheKey, entry); err != nil {
			log.Printf("[orchestrator] cache set failed: %v", err)
		}
	}

	s.recordUsage(ctx, usage.Record{
		TenantID:     t.ID,
		Model:        result.Model,
		Provider:     result.Provider,
		InputTokens:  result.PromptTokens,
		OutputTokens: result.CompletionTokens,
		LatencyMs:    latency.Milliseconds(),
		Cost:         0, // Ollama is local; real cost lands with paid providers.
		Cached:       false,
	})

	log.Printf("[orchestrator] inference complete | tenant=%s model=%s tokens=%d latency_ms=%d",
		t.ID, result.Model, result.TotalTokens, latency.Milliseconds())

	return &pb.ExecuteInferenceResponse{
		Output:   result.Output,
		Model:    result.Model,
		Provider: result.Provider,
		Usage: &pb.TokenUsage{
			PromptTokens:     result.PromptTokens,
			CompletionTokens: result.CompletionTokens,
			TotalTokens:      result.TotalTokens,
		},
		Cached: false,
	}, nil
}

// recordUsage writes a usage row best-effort. Failures are logged but do not
// fail the request; usage is observability data, not part of the contract.
func (s *Service) recordUsage(ctx context.Context, rec usage.Record) {
	if s.usageRepo == nil {
		return
	}
	if err := s.usageRepo.Insert(ctx, rec); err != nil {
		log.Printf("[orchestrator] usage insert failed (non-fatal): %v", err)
	}
}

// HealthCheck returns the health status of the orchestrator and its provider.
func (s *Service) HealthCheck(ctx context.Context, _ *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
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
