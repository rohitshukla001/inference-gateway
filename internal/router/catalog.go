// Package router selects a provider and model for a request from tenant
// policy, cost and latency constraints.
package router

import "github.com/rshuklas/inference-gateway/internal/provider"

// Tier is a coarse pricing/quality bucket for routing decisions.
type Tier string

// Built-in tiers. A tenant's policy picks the preferred tier; request hints
// (max_cost, latency_sla_ms) filter candidates first.
const (
	TierCheap   Tier = "cheap"
	TierDefault Tier = "default"
	TierPremium Tier = "premium"
)

// ModelEntry is a routable (provider, model). Pricing duplicates the
// provider's Pricing() so routing is self-contained;
// TestCatalogPricingMatchesProviders catches drift.
type ModelEntry struct {
	Provider     string
	Model        string
	Tier         Tier
	Pricing      provider.Pricing
	LatencyP95Ms int
}

// Catalog is an ordered list of routable models. Order breaks price ties.
type Catalog []ModelEntry

// DefaultCatalog returns the built-in static catalog. It always includes the
// free local Ollama model as the fallback target.
func DefaultCatalog() Catalog {
	return Catalog{
		// Local and free; the universal fallback.
		{
			Provider:     "ollama",
			Model:        "llama3.2",
			Tier:         TierCheap,
			Pricing:      provider.Pricing{},
			LatencyP95Ms: 15000,
		},
		{
			Provider:     "mock",
			Model:        "gpt-4o-mini",
			Tier:         TierDefault,
			Pricing:      provider.Pricing{InputPer1k: 0.00015, OutputPer1k: 0.0006},
			LatencyP95Ms: 200,
		},
		{
			Provider:     "mock",
			Model:        "gpt-4o",
			Tier:         TierPremium,
			Pricing:      provider.Pricing{InputPer1k: 0.0025, OutputPer1k: 0.01},
			LatencyP95Ms: 400,
		},
	}
}

// ByModel returns the catalog entry for an exact model name, if present.
func (c Catalog) ByModel(model string) (ModelEntry, bool) {
	for _, e := range c {
		if e.Model == model {
			return e, true
		}
	}
	return ModelEntry{}, false
}

// ByTier returns all entries in tier in catalog order.
func (c Catalog) ByTier(tier Tier) Catalog {
	out := make(Catalog, 0, len(c))
	for _, e := range c {
		if e.Tier == tier {
			out = append(out, e)
		}
	}
	return out
}

// FallbackOllama returns the first Ollama entry, the local fallback when the
// primary fails.
func (c Catalog) FallbackOllama() (ModelEntry, bool) {
	for _, e := range c {
		if e.Provider == "ollama" {
			return e, true
		}
	}
	return ModelEntry{}, false
}
