// Package router selects a provider + model for an inference request based on
// tenant policy, cost, and latency constraints.
package router

import "github.com/rshuklas/inference-gateway/internal/provider"

// Tier is a coarse pricing/quality bucket for routing decisions.
type Tier string

// Built-in tiers. Tenants pick a default tier in their policy; the router
// honours request-time hints (max_cost, latency_sla_ms) to override it.
const (
	TierCheap   Tier = "cheap"
	TierDefault Tier = "default"
	TierPremium Tier = "premium"
)

// ModelEntry describes a single (provider, model) the router can select.
// Pricing is duplicated from the provider's own Pricing() method to keep
// route-time decisions self-contained; the two must stay in sync.
type ModelEntry struct {
	Provider     string
	Model        string
	Tier         Tier
	Pricing      provider.Pricing
	LatencyP95Ms int
}

// Catalog is an ordered list of routable models. Order matters for tie-
// breaking when multiple models satisfy a constraint equally.
type Catalog []ModelEntry

// DefaultCatalog returns the static catalog used in dev. The local Ollama
// model is always present as a zero-cost fallback target.
func DefaultCatalog() Catalog {
	return Catalog{
		// Local, free, slower. Used as the universal fallback.
		{
			Provider:     "ollama",
			Model:        "llama3.2",
			Tier:         TierCheap,
			Pricing:      provider.Pricing{},
			LatencyP95Ms: 15000,
		},
		// Cheap cloud-style. Default tier for most workloads.
		{
			Provider:     "mock",
			Model:        "gpt-4o-mini",
			Tier:         TierDefault,
			Pricing:      provider.Pricing{InputPer1k: 0.00015, OutputPer1k: 0.0006},
			LatencyP95Ms: 200,
		},
		// Premium cloud-style. Higher quality, higher cost.
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

// FallbackOllama returns the first ollama-backed entry in the catalog, used as
// the always-available local fallback when the primary provider fails.
func (c Catalog) FallbackOllama() (ModelEntry, bool) {
	for _, e := range c {
		if e.Provider == "ollama" {
			return e, true
		}
	}
	return ModelEntry{}, false
}
