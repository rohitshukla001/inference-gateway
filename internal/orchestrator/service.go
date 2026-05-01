// Package orchestrator implements the gRPC InferenceService that coordinates
// between the gateway and LLM providers.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/rshuklas/inference-gateway/internal/cache/responsecache"
	"github.com/rshuklas/inference-gateway/internal/policy"
	"github.com/rshuklas/inference-gateway/internal/provider"
	"github.com/rshuklas/inference-gateway/internal/router"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	"github.com/rshuklas/inference-gateway/internal/usage"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const serviceVersion = "0.3.0"

// Service implements the gRPC InferenceService.
type Service struct {
	pb.UnimplementedInferenceServiceServer

	providers  map[string]provider.Provider
	cache      *responsecache.Cache
	usageRepo  *usage.Repository
	router     *router.Router
	policyRepo *policy.Repository
}

// Deps groups the runtime dependencies the service needs. Using a struct
// keeps NewService stable as we add Phase 3+ capabilities.
type Deps struct {
	Providers  map[string]provider.Provider
	Cache      *responsecache.Cache
	UsageRepo  *usage.Repository
	Router     *router.Router
	PolicyRepo *policy.Repository
}

// NewService constructs the orchestrator with its dependencies. Providers must
// contain at least one entry; the rest are optional and may be nil for tests.
func NewService(d Deps) *Service {
	return &Service{
		providers:  d.Providers,
		cache:      d.Cache,
		usageRepo:  d.UsageRepo,
		router:     d.Router,
		policyRepo: d.PolicyRepo,
	}
}

// cachedEntry is the on-wire shape of a cached response. Mirrors the fields we
// hand back to clients.
type cachedEntry struct {
	Output           string `json:"output"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	PromptTokens     int32  `json:"prompt_tokens"`
	CompletionTokens int32  `json:"completion_tokens"`
	TotalTokens      int32  `json:"total_tokens"`
}

// ExecuteInference routes the request, attempts the cache, calls the chosen
// provider (with a 1-deep fallback), and records usage.
func (s *Service) ExecuteInference(ctx context.Context, req *pb.ExecuteInferenceRequest) (*pb.ExecuteInferenceResponse, error) {
	if req.GetPrompt() == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt is required")
	}

	t := tenant.FromContext(ctx)
	if t == nil {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	// Tenant policy. Missing is treated as "no constraints" so dev tenants
	// without a row keep working; production rollouts will require one.
	pol, err := s.loadPolicy(ctx, t.ID)
	if err != nil {
		return nil, err
	}

	// Routing decision.
	decision, err := s.router.Route(router.Inputs{
		ExplicitModel: req.GetModel(),
		MaxCost:       req.GetMaxCost(),
		LatencySLAMs:  req.GetLatencySlaMs(),
		Prompt:        req.GetPrompt(),
	}, pol)
	if err != nil {
		return nil, mapRoutingError(err)
	}

	log.Printf("[orchestrator] route decision | tenant=%s primary=%s/%s fallback=%s",
		t.ID, decision.Primary.Provider, decision.Primary.Model, fallbackName(decision.Fallback))

	start := time.Now()
	cacheKey := responsecache.Key(t.ID, decision.Primary.Model, req.GetPrompt())

	// Cache lookup keyed on the routed primary so policy-driven changes
	// don't return stale results from a different model.
	if hit, resp, ok := s.tryCache(ctx, cacheKey); ok {
		latency := time.Since(start)
		log.Printf("[orchestrator] cache HIT | tenant=%s model=%s latency_ms=%d",
			t.ID, hit.Model, latency.Milliseconds())

		s.recordUsage(ctx, usage.Record{
			TenantID:     t.ID,
			Model:        hit.Model,
			Provider:     hit.Provider,
			InputTokens:  hit.PromptTokens,
			OutputTokens: hit.CompletionTokens,
			LatencyMs:    latency.Milliseconds(),
			Cost:         0,
			Cached:       true,
		})
		return resp, nil
	}

	log.Printf("[orchestrator] cache MISS | tenant=%s model=%s", t.ID, decision.Primary.Model)

	// Primary attempt. On error, fall back if available.
	served, result, err := s.executeWithFallback(ctx, decision, req.GetPrompt())
	if err != nil {
		log.Printf("[orchestrator] all providers failed: %v", err)
		return nil, status.Error(codes.Internal, fmt.Sprintf("provider execution failed: %v", err))
	}
	latency := time.Since(start)

	// Only cache when the primary served the response. A fallback means the
	// primary was sick; caching its substitute under the primary key would
	// mislead future routing.
	if served.Provider == decision.Primary.Provider && served.Model == decision.Primary.Model {
		s.storeCache(ctx, cacheKey, result)
	}

	cost := served.Pricing.Cost(result.PromptTokens, result.CompletionTokens)

	s.recordUsage(ctx, usage.Record{
		TenantID:     t.ID,
		Model:        result.Model,
		Provider:     result.Provider,
		InputTokens:  result.PromptTokens,
		OutputTokens: result.CompletionTokens,
		LatencyMs:    latency.Milliseconds(),
		Cost:         cost,
		Cached:       false,
	})

	log.Printf("[orchestrator] inference complete | tenant=%s model=%s tokens=%d cost=%.6f latency_ms=%d",
		t.ID, result.Model, result.TotalTokens, cost, latency.Milliseconds())

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

// executeWithFallback runs decision.Primary, then decision.Fallback if the
// primary errors. It returns the catalog entry that actually served the
// request (so cost is computed against the right rates).
func (s *Service) executeWithFallback(
	ctx context.Context,
	decision router.Decision,
	prompt string,
) (router.ModelEntry, *provider.InferenceResponse, error) {
	primaryProv, ok := s.providers[decision.Primary.Provider]
	if ok {
		result, err := primaryProv.Execute(ctx, &provider.InferenceRequest{
			Prompt: prompt,
			Model:  decision.Primary.Model,
		})
		if err == nil {
			return decision.Primary, result, nil
		}
		log.Printf("[orchestrator] primary %s/%s failed: %v",
			decision.Primary.Provider, decision.Primary.Model, err)
	} else {
		log.Printf("[orchestrator] primary provider %q not in registry", decision.Primary.Provider)
	}

	if decision.Fallback == nil {
		return router.ModelEntry{}, nil, fmt.Errorf("primary failed and no fallback configured")
	}

	fbProv, ok := s.providers[decision.Fallback.Provider]
	if !ok {
		return router.ModelEntry{}, nil, fmt.Errorf("fallback provider %q not in registry", decision.Fallback.Provider)
	}

	log.Printf("[orchestrator] fallback to %s/%s", decision.Fallback.Provider, decision.Fallback.Model)
	result, err := fbProv.Execute(ctx, &provider.InferenceRequest{
		Prompt: prompt,
		Model:  decision.Fallback.Model,
	})
	if err != nil {
		return router.ModelEntry{}, nil, fmt.Errorf("fallback also failed: %w", err)
	}
	return *decision.Fallback, result, nil
}

func (s *Service) loadPolicy(ctx context.Context, tenantID string) (*policy.Policy, error) {
	if s.policyRepo == nil {
		return nil, nil
	}
	pol, err := s.policyRepo.Get(ctx, tenantID)
	if err != nil {
		if errors.Is(err, policy.ErrNotFound) {
			return nil, nil
		}
		return nil, status.Errorf(codes.Internal, "load policy: %v", err)
	}
	return pol, nil
}

// tryCache returns (entry, response, true) on hit. Errors from the cache
// layer are logged and treated as misses so a Redis blip never fails a call.
func (s *Service) tryCache(ctx context.Context, key string) (cachedEntry, *pb.ExecuteInferenceResponse, bool) {
	if s.cache == nil {
		return cachedEntry{}, nil, false
	}
	var entry cachedEntry
	hit, err := s.cache.Get(ctx, key, &entry)
	if err != nil {
		log.Printf("[orchestrator] cache get failed: %v", err)
		return cachedEntry{}, nil, false
	}
	if !hit {
		return cachedEntry{}, nil, false
	}
	return entry, &pb.ExecuteInferenceResponse{
		Output:   entry.Output,
		Model:    entry.Model,
		Provider: entry.Provider,
		Usage: &pb.TokenUsage{
			PromptTokens:     entry.PromptTokens,
			CompletionTokens: entry.CompletionTokens,
			TotalTokens:      entry.TotalTokens,
		},
		Cached: true,
	}, true
}

func (s *Service) storeCache(ctx context.Context, key string, result *provider.InferenceResponse) {
	if s.cache == nil {
		return
	}
	entry := cachedEntry{
		Output:           result.Output,
		Model:            result.Model,
		Provider:         result.Provider,
		PromptTokens:     result.PromptTokens,
		CompletionTokens: result.CompletionTokens,
		TotalTokens:      result.TotalTokens,
	}
	if err := s.cache.Set(ctx, key, entry); err != nil {
		log.Printf("[orchestrator] cache set failed: %v", err)
	}
}

func (s *Service) recordUsage(ctx context.Context, rec usage.Record) {
	if s.usageRepo == nil {
		return
	}
	if err := s.usageRepo.Insert(ctx, rec); err != nil {
		log.Printf("[orchestrator] usage insert failed (non-fatal): %v", err)
	}
}

// HealthCheck reports SERVING when at least one provider is reachable.
func (s *Service) HealthCheck(ctx context.Context, _ *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	healthStatus := "NOT_SERVING"
	for name, p := range s.providers {
		if err := p.HealthCheck(ctx); err == nil {
			healthStatus = "SERVING"
			break
		} else {
			log.Printf("[orchestrator] provider %s unhealthy: %v", name, err)
		}
	}
	return &pb.HealthCheckResponse{
		Status:  healthStatus,
		Service: "orchestrator",
		Version: serviceVersion,
	}, nil
}

func mapRoutingError(err error) error {
	switch {
	case errors.Is(err, router.ErrModelNotAllowed):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, router.ErrModelNotInCatalog), errors.Is(err, router.ErrNoCandidates):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Errorf(codes.Internal, "routing failed: %v", err)
	}
}

func fallbackName(fb *router.ModelEntry) string {
	if fb == nil {
		return "(none)"
	}
	return fmt.Sprintf("%s/%s", fb.Provider, fb.Model)
}
