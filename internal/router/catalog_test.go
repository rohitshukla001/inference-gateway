package router

import "testing"

func TestDefaultCatalog_ContainsExpectedTiers(t *testing.T) {
	c := DefaultCatalog()
	tiers := map[Tier]bool{}
	for _, e := range c {
		tiers[e.Tier] = true
	}
	for _, want := range []Tier{TierCheap, TierDefault, TierPremium} {
		if !tiers[want] {
			t.Errorf("expected tier %s in default catalog", want)
		}
	}
}

func TestByModel(t *testing.T) {
	c := DefaultCatalog()
	got, ok := c.ByModel("gpt-4o-mini")
	if !ok {
		t.Fatal("expected gpt-4o-mini in catalog")
	}
	if got.Tier != TierDefault {
		t.Errorf("expected gpt-4o-mini in TierDefault, got %s", got.Tier)
	}
}

func TestByModel_Unknown(t *testing.T) {
	c := DefaultCatalog()
	_, ok := c.ByModel("not-a-model")
	if ok {
		t.Error("expected ok=false for unknown model")
	}
}

func TestByTier(t *testing.T) {
	c := DefaultCatalog()
	cheap := c.ByTier(TierCheap)
	if len(cheap) == 0 {
		t.Fatal("expected at least one cheap entry")
	}
	for _, e := range cheap {
		if e.Tier != TierCheap {
			t.Errorf("ByTier returned non-matching tier %s", e.Tier)
		}
	}
}

func TestFallbackOllama_Present(t *testing.T) {
	c := DefaultCatalog()
	got, ok := c.FallbackOllama()
	if !ok {
		t.Fatal("expected ollama fallback in default catalog")
	}
	if got.Provider != "ollama" {
		t.Errorf("expected provider ollama, got %s", got.Provider)
	}
}
