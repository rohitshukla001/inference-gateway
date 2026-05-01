package router

import (
	"errors"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/policy"
)

func TestRoute_ExplicitModel_Allowed(t *testing.T) {
	r := New(DefaultCatalog())
	pol := &policy.Policy{AllowedModels: []string{"gpt-4o"}, DefaultTier: "premium"}

	d, err := r.Route(Inputs{ExplicitModel: "gpt-4o"}, pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Primary.Model != "gpt-4o" {
		t.Errorf("expected primary gpt-4o, got %s", d.Primary.Model)
	}
	if d.Fallback == nil || d.Fallback.Provider != "ollama" {
		t.Errorf("expected ollama fallback, got %+v", d.Fallback)
	}
}

func TestRoute_ExplicitModel_NotAllowed(t *testing.T) {
	r := New(DefaultCatalog())
	pol := &policy.Policy{AllowedModels: []string{"llama3.2"}}

	_, err := r.Route(Inputs{ExplicitModel: "gpt-4o"}, pol)
	if !errors.Is(err, ErrModelNotAllowed) {
		t.Errorf("expected ErrModelNotAllowed, got %v", err)
	}
}

func TestRoute_ExplicitModel_NotInCatalog(t *testing.T) {
	r := New(DefaultCatalog())
	pol := &policy.Policy{}

	_, err := r.Route(Inputs{ExplicitModel: "made-up-model"}, pol)
	if !errors.Is(err, ErrModelNotInCatalog) {
		t.Errorf("expected ErrModelNotInCatalog, got %v", err)
	}
}

func TestRoute_NoHints_PrefersDefaultTier(t *testing.T) {
	r := New(DefaultCatalog())
	pol := &policy.Policy{DefaultTier: "default"}

	d, err := r.Route(Inputs{Prompt: "hi"}, pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Primary.Tier != TierDefault {
		t.Errorf("expected primary in default tier, got %s", d.Primary.Tier)
	}
}

func TestRoute_PremiumTier_PicksPremium(t *testing.T) {
	r := New(DefaultCatalog())
	pol := &policy.Policy{DefaultTier: "premium"}

	d, err := r.Route(Inputs{Prompt: "hi"}, pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Primary.Tier != TierPremium {
		t.Errorf("expected premium tier, got %s", d.Primary.Tier)
	}
}

func TestRoute_LatencySLA_FiltersSlowModels(t *testing.T) {
	r := New(DefaultCatalog())
	pol := &policy.Policy{}

	// 1000ms SLA excludes the local Ollama (15s p95) but allows mocks.
	d, err := r.Route(Inputs{LatencySLAMs: 1000, Prompt: "hi"}, pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Primary.Provider == "ollama" {
		t.Errorf("ollama should be filtered out by 1s SLA, got %+v", d.Primary)
	}
}

func TestRoute_TightLatencySLA_NoCandidates(t *testing.T) {
	r := New(DefaultCatalog())
	pol := &policy.Policy{}

	// 50ms SLA excludes everything in the default catalog.
	_, err := r.Route(Inputs{LatencySLAMs: 50, Prompt: "hi"}, pol)
	if !errors.Is(err, ErrNoCandidates) {
		t.Errorf("expected ErrNoCandidates for tight SLA, got %v", err)
	}
}

func TestRoute_MaxCost_PrefersFreeOllama(t *testing.T) {
	r := New(DefaultCatalog())
	pol := &policy.Policy{}

	// $0.0001 budget excludes anything with non-zero rates against any
	// realistic prompt. Only ollama (zero rates) survives.
	d, err := r.Route(Inputs{MaxCost: 0.0001, Prompt: "tell me a story about cats", LatencySLAMs: 0}, pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Primary.Provider != "ollama" {
		t.Errorf("expected ollama under tight budget, got %s/%s", d.Primary.Provider, d.Primary.Model)
	}
}

func TestRoute_OllamaPrimary_NoFallback(t *testing.T) {
	r := New(DefaultCatalog())
	pol := &policy.Policy{}

	d, err := r.Route(Inputs{ExplicitModel: "llama3.2"}, pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Fallback != nil {
		t.Errorf("ollama-primary should not have a fallback, got %+v", d.Fallback)
	}
}
