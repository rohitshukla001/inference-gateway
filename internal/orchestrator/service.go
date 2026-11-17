// Package orchestrator implements the gRPC InferenceService between the
// gateway and the LLM providers.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/rshuklas/inference-gateway/internal/budget"
	"github.com/rshuklas/inference-gateway/internal/cache/responsecache"
	"github.com/rshuklas/inference-gateway/internal/observability/metrics"
	"github.com/rshuklas/inference-gateway/internal/policy"
	"github.com/rshuklas/inference-gateway/internal/provider"
	"github.com/rshuklas/inference-gateway/internal/router"
	"github.com/rshuklas/inference-gateway/internal/tenant"
	"github.com/rshuklas/inference-gateway/internal/usage"
	pb "github.com/rshuklas/inference-gateway/proto/inference/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var tracer = otel.Tracer("orchestrator")

const serviceVersion = "0.6.0"

// HealthSource reports whether a provider is currently healthy. It lets the
// service skip a known-down primary instead of spending retries on it.
type HealthSource interface {
	IsHealthy(name string) bool
}

// PolicyReader is the slice of policy.Repository the service needs, so the
// Postgres repository or its cached wrapper can be used.
type PolicyReader interface {
	Get(ctx context.Context, tenantID string) (*policy.Policy, error)
}

// BudgetReader is the slice of budget.Repository the service needs. GetLimits
// is cache-friendly; Reserve and Settle change live spend.
type BudgetReader interface {
	GetLimits(ctx context.Context, tenantID string) (*budget.Limits, error)
	Reserve(ctx context.Context, tenantID string, amount, limit float64) (*budget.Reservation, error)
	Settle(ctx context.Context, res *budget.Reservation, actual float64) error
}

// Service implements the gRPC InferenceService.
type Service struct {
	pb.UnimplementedInferenceServiceServer

	providers  map[string]provider.Provider
	cache      *responsecache.Cache
	usageRepo  *usage.Repository
	router     *router.Router
	policyRepo PolicyReader
	budgetRepo BudgetReader
	metrics    *metrics.Metrics
	retry      RetryConfig
	health     HealthSource
}

// Deps groups the service's runtime dependencies.
type Deps struct {
	Providers  map[string]provider.Provider
	Cache      *responsecache.Cache
	UsageRepo  *usage.Repository
	Router     *router.Router
	PolicyRepo PolicyReader
	BudgetRepo BudgetReader
	Metrics    *metrics.Metrics
	Retry      RetryConfig
	Health     HealthSource
}

// NewService builds a Service. Providers must have at least one entry; the
// other dependencies may be nil.
func NewService(d Deps) *Service {
	return &Service{
		providers:  d.Providers,
		cache:      d.Cache,
		usageRepo:  d.UsageRepo,
		router:     d.Router,
		policyRepo: d.PolicyRepo,
		budgetRepo: d.BudgetRepo,
		metrics:    d.Metrics,
		retry:      d.Retry,
		health:     d.Health,
	}
}

// cachedEntry is the JSON shape of a cached response.
type cachedEntry struct {
	Output           string `json:"output"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	PromptTokens     int32  `json:"prompt_tokens"`
	CompletionTokens int32  `json:"completion_tokens"`
	TotalTokens      int32  `json:"total_tokens"`
}

// ExecuteInference routes the request, checks the cache, reserves budget,
// calls the primary (falling back once), settles the budget and records usage.
func (s *Service) ExecuteInference(ctx context.Context, req *pb.ExecuteInferenceRequest) (*pb.ExecuteInferenceResponse, error) {
	if req.GetPrompt() == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt is required")
	}

	t := tenant.FromContext(ctx)
	if t == nil {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	// A missing policy row means no constraints.
	pol, err := s.loadPolicy(ctx, t.ID)
	if err != nil {
		return nil, err
	}

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

	// Keyed on the routed primary model so a routing change never returns
	// another model's cached output.
	if hit, resp, ok := s.tryCache(ctx, cacheKey); ok {
		latency := time.Since(start)
		log.Printf("[orchestrator] cache HIT | tenant=%s model=%s latency_ms=%d",
			t.ID, hit.Model, latency.Milliseconds())

		s.observeCacheHit(t.ID, hit.Model)
		s.observeLatency(t.ID, hit.Model, hit.Provider, "cached", latency)

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

	s.observeCacheMiss(t.ID, decision.Primary.Model)
	log.Printf("[orchestrator] cache MISS | tenant=%s model=%s", t.ID, decision.Primary.Model)

	// Cache hits return above, so only calls that reach a provider are gated.
	reservation, err := s.reserveBudget(ctx, t.ID, decision.Primary, req.GetPrompt())
	if err != nil {
		return nil, err
	}

	served, result, err := s.executeWithFallback(ctx, decision, req.GetPrompt())
	if err != nil {
		s.settleBudget(ctx, reservation, 0)
		s.observeLatency(t.ID, decision.Primary.Model, decision.Primary.Provider, "error", time.Since(start))
		log.Printf("[orchestrator] all providers failed: %v", err)
		return nil, status.Error(codes.Internal, fmt.Sprintf("provider execution failed: %v", err))
	}
	latency := time.Since(start)

	// Cache only primary responses: the key names the primary model, so a
	// cached fallback response would be served as the primary's output.
	if served.Provider == decision.Primary.Provider && served.Model == decision.Primary.Model {
		s.storeCache(ctx, cacheKey, result)
	}

	cost := served.Pricing.Cost(result.PromptTokens, result.CompletionTokens)
	s.settleBudget(ctx, reservation, cost)

	s.observeLatency(t.ID, result.Model, result.Provider, "ok", latency)
	s.observeCost(t.ID, result.Model, result.Provider, cost)

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
		Cost:   cost,
	}, nil
}

// reserveBudget holds the projected cost of this call against the tenant's
// monthly limit, rejecting with ResourceExhausted if it does not fit. It
// returns a nil reservation when no gate applies: free models, tenants
// without a budget row, or a zero ("unlimited") limit. If the budget store
// is unreachable, paid calls are refused rather than let through uncapped.
func (s *Service) reserveBudget(
	ctx context.Context,
	tenantID string,
	primary router.ModelEntry,
	prompt string,
) (*budget.Reservation, error) {
	if s.budgetRepo == nil {
		return nil, nil
	}
	projected := router.EstimateCost(prompt, primary.Pricing)
	if projected <= 0 {
		return nil, nil
	}
	limits, err := s.budgetRepo.GetLimits(ctx, tenantID)
	if err != nil {
		if errors.Is(err, budget.ErrNotFound) {
			return nil, nil
		}
		log.Printf("[orchestrator] budget lookup failed (refusing paid call): %v", err)
		return nil, status.Error(codes.Unavailable, "budget check unavailable")
	}
	// A zero monthly limit means unlimited.
	if limits.MonthlyLimitUSD <= 0 {
		return nil, nil
	}

	res, err := s.budgetRepo.Reserve(ctx, tenantID, projected, limits.MonthlyLimitUSD)
	if err != nil {
		var over *budget.OverBudgetError
		if errors.As(err, &over) {
			log.Printf("[orchestrator] budget BLOCK | tenant=%s mtd=%.4f projected=%.4f limit=%.4f",
				tenantID, over.SpentUSD, projected, limits.MonthlyLimitUSD)
			return nil, status.Errorf(codes.ResourceExhausted,
				"monthly budget exceeded: spent $%.4f of $%.2f, projected $%.4f more",
				over.SpentUSD, limits.MonthlyLimitUSD, projected)
		}
		if errors.Is(err, budget.ErrOverBudget) {
			return nil, status.Error(codes.ResourceExhausted, "monthly budget exceeded")
		}
		log.Printf("[orchestrator] budget reserve failed (refusing paid call): %v", err)
		return nil, status.Error(codes.Unavailable, "budget check unavailable")
	}

	if limits.SoftLimitUSD > 0 && res.SpentUSD > limits.SoftLimitUSD {
		log.Printf("[orchestrator] budget WARN | tenant=%s mtd=%.4f soft=%.4f",
			tenantID, res.SpentUSD, limits.SoftLimitUSD)
	}
	return res, nil
}

// settleBudget swaps a reservation's projected amount for the actual cost.
// It runs even if the caller has gone away, so spend stays accurate.
func (s *Service) settleBudget(ctx context.Context, res *budget.Reservation, actual float64) {
	if res == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.budgetRepo.Settle(ctx, res, actual); err != nil {
		log.Printf("[orchestrator] budget settle failed | tenant=%s reserved=%.6f actual=%.6f: %v",
			res.TenantID, res.Amount, actual, err)
	}
}

// executeWithFallback runs decision.Primary, then decision.Fallback if the
// primary fails or is known unhealthy. It returns the entry that served the
// request so cost uses that model's rates.
func (s *Service) executeWithFallback(
	ctx context.Context,
	decision router.Decision,
	prompt string,
) (router.ModelEntry, *provider.InferenceResponse, error) {
	ctx, span := tracer.Start(ctx, "provider.execute", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("provider", decision.Primary.Provider),
			attribute.String("model", decision.Primary.Model),
		))
	defer span.End()

	primaryProv, ok := s.providers[decision.Primary.Provider]
	skipPrimary := s.health != nil && decision.Fallback != nil && !s.health.IsHealthy(decision.Primary.Provider)
	if skipPrimary {
		s.observeProviderFailure(decision.Primary.Provider, decision.Primary.Model)
		log.Printf("[orchestrator] primary %s skipped: known unhealthy",
			decision.Primary.Provider)
	} else if ok {
		result, err := s.executeWithRetry(ctx, primaryProv, &provider.InferenceRequest{
			Prompt:    prompt,
			Model:     decision.Primary.Model,
			MaxTokens: router.MaxOutputTokens,
		})
		if err == nil {
			return decision.Primary, result, nil
		}
		s.observeProviderFailure(decision.Primary.Provider, decision.Primary.Model)
		log.Printf("[orchestrator] primary %s/%s failed: %v",
			decision.Primary.Provider, decision.Primary.Model, err)
	} else {
		s.observeProviderFailure(decision.Primary.Provider, decision.Primary.Model)
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
	result, err := s.executeWithRetry(ctx, fbProv, &provider.InferenceRequest{
		Prompt:    prompt,
		Model:     decision.Fallback.Model,
		MaxTokens: router.MaxOutputTokens,
	})
	if err != nil {
		s.observeProviderFailure(decision.Fallback.Provider, decision.Fallback.Model)
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
	ctx, span := tracer.Start(ctx, "cache.lookup", trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	var entry cachedEntry
	hit, err := s.cache.Get(ctx, key, &entry)
	span.SetAttributes(attribute.Bool("cache.hit", hit))
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

// observe* helpers record Prometheus metrics and no-op when metrics is nil.
func (s *Service) observeLatency(tenantID, model, provider, statusLabel string, latency time.Duration) {
	if s.metrics == nil {
		return
	}
	s.metrics.InferenceLatency.WithLabelValues(tenantID, model, provider, statusLabel).Observe(latency.Seconds())
}

func (s *Service) observeCacheHit(tenantID, model string) {
	if s.metrics == nil {
		return
	}
	s.metrics.CacheHits.WithLabelValues(tenantID, model).Inc()
}

func (s *Service) observeCacheMiss(tenantID, model string) {
	if s.metrics == nil {
		return
	}
	s.metrics.CacheMisses.WithLabelValues(tenantID, model).Inc()
}

func (s *Service) observeProviderFailure(provider, model string) {
	if s.metrics == nil {
		return
	}
	s.metrics.ProviderFailures.WithLabelValues(provider, model).Inc()
}

func (s *Service) observeCost(tenantID, model, provider string, cost float64) {
	if s.metrics == nil || cost <= 0 {
		return
	}
	s.metrics.InferenceCost.WithLabelValues(tenantID, model, provider).Add(cost)
}

func (s *Service) recordUsage(ctx context.Context, rec usage.Record) {
	if s.usageRepo == nil {
		return
	}
	// The provider call already happened; record it even if the caller is gone.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.usageRepo.Insert(ctx, rec); err != nil {
		log.Printf("[orchestrator] usage insert failed (non-fatal): %v", err)
	}
}

// HealthCheck reports SERVING when at least one provider is healthy. With a
// background checker wired it answers from that, so the unauthenticated
// gateway /health endpoint never triggers provider calls; otherwise it probes
// each provider directly.
func (s *Service) HealthCheck(ctx context.Context, _ *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	healthStatus := "NOT_SERVING"
	if s.health != nil {
		for name := range s.providers {
			if s.health.IsHealthy(name) {
				healthStatus = "SERVING"
				break
			}
		}
		return &pb.HealthCheckResponse{Status: healthStatus, Service: "orchestrator", Version: serviceVersion}, nil
	}
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
