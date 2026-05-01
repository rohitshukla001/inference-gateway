package router

import (
	"errors"
	"fmt"

	"github.com/rshuklas/inference-gateway/internal/policy"
)

// Inputs are the request-time hints the router uses to pick a model.
type Inputs struct {
	ExplicitModel string
	MaxCost       float64
	LatencySLAMs  int32
	Prompt        string
}

// Decision is the chosen primary model plus an optional fallback the
// orchestrator should retry against if the primary fails.
type Decision struct {
	Primary  ModelEntry
	Fallback *ModelEntry
}

// Sentinel errors. The orchestrator maps these to gRPC codes.
var (
	ErrModelNotAllowed   = errors.New("model not allowed for tenant")
	ErrModelNotInCatalog = errors.New("model not in router catalog")
	ErrNoCandidates      = errors.New("no model satisfies routing constraints")
)

// Router picks one ModelEntry from a catalog given the inputs and tenant
// policy. The selection algorithm is intentionally simple and deterministic
// so failures are easy to explain.
type Router struct {
	catalog Catalog
}

// New returns a Router using catalog as its source of truth.
func New(catalog Catalog) *Router {
	return &Router{catalog: catalog}
}

// Route applies the routing rules:
//
//  1. If ExplicitModel is set, use it (after a policy + catalog check).
//  2. Otherwise, filter the catalog by policy + budget + SLA constraints,
//     prefer the tenant's default_tier, then break ties by input price.
//  3. The fallback, if available, is the local Ollama entry, unless the
//     primary is already Ollama.
func (r *Router) Route(in Inputs, pol *policy.Policy) (Decision, error) {
	// Path 1: explicit model.
	if in.ExplicitModel != "" {
		if pol != nil && !pol.AllowsModel(in.ExplicitModel) {
			return Decision{}, fmt.Errorf("%w: %s", ErrModelNotAllowed, in.ExplicitModel)
		}
		entry, ok := r.catalog.ByModel(in.ExplicitModel)
		if !ok {
			return Decision{}, fmt.Errorf("%w: %s", ErrModelNotInCatalog, in.ExplicitModel)
		}
		return r.withFallback(entry), nil
	}

	// Path 2: rule-based selection.
	candidates := r.candidates(in, pol)
	if len(candidates) == 0 {
		return Decision{}, ErrNoCandidates
	}

	primary := pickPrimary(candidates, defaultTier(pol))
	return r.withFallback(primary), nil
}

func (r *Router) candidates(in Inputs, pol *policy.Policy) Catalog {
	out := make(Catalog, 0, len(r.catalog))
	for _, e := range r.catalog {
		if pol != nil && !pol.AllowsModel(e.Model) {
			continue
		}
		if in.LatencySLAMs > 0 && e.LatencyP95Ms > int(in.LatencySLAMs) {
			continue
		}
		if in.MaxCost > 0 {
			projected := EstimateCost(in.Prompt, e.Pricing)
			if projected > in.MaxCost {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// withFallback decorates a primary with a local-Ollama fallback when
// available and distinct. A single-deep chain is enough to handle "primary
// provider down" without inviting cascading retries.
func (r *Router) withFallback(primary ModelEntry) Decision {
	if primary.Provider == "ollama" {
		return Decision{Primary: primary}
	}
	fb, ok := r.catalog.FallbackOllama()
	if !ok {
		return Decision{Primary: primary}
	}
	return Decision{Primary: primary, Fallback: &fb}
}

// pickPrimary chooses the best candidate. Preference order:
//  1. Entries matching the tenant's default tier
//  2. Among matches, lowest input price (cheap == cheap, deterministic)
//  3. Among ties, first in catalog order
func pickPrimary(cands Catalog, prefTier Tier) ModelEntry {
	tiered := cands.ByTier(prefTier)
	if len(tiered) == 0 {
		tiered = cands
	}

	best := tiered[0]
	for _, e := range tiered[1:] {
		if e.Pricing.InputPer1k < best.Pricing.InputPer1k {
			best = e
		}
	}
	return best
}

func defaultTier(pol *policy.Policy) Tier {
	if pol == nil || pol.DefaultTier == "" {
		return TierDefault
	}
	return Tier(pol.DefaultTier)
}
