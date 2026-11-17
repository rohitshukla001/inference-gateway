package router_test

import (
	"testing"

	"github.com/rshuklas/inference-gateway/internal/provider"
	"github.com/rshuklas/inference-gateway/internal/provider/mock"
	"github.com/rshuklas/inference-gateway/internal/provider/ollama"
	"github.com/rshuklas/inference-gateway/internal/router"
)

// The catalog carries its own copy of each model's pricing so routing stays
// self-contained. This test fails if that copy drifts from the provider's
// own rates, which would make budget projections and billed cost disagree.
func TestCatalogPricingMatchesProviders(t *testing.T) {
	providers := map[string]provider.Provider{
		"mock":   mock.New(),
		"ollama": ollama.New("http://unused", "llama3.2", 0),
	}
	for _, e := range router.DefaultCatalog() {
		p, ok := providers[e.Provider]
		if !ok {
			t.Fatalf("catalog entry %s/%s has no provider", e.Provider, e.Model)
		}
		got, ok := p.Pricing(e.Model)
		if !ok {
			t.Fatalf("provider %s does not know model %s", e.Provider, e.Model)
		}
		if got != e.Pricing {
			t.Errorf("%s/%s: catalog pricing %+v, provider pricing %+v", e.Provider, e.Model, e.Pricing, got)
		}
	}
}
