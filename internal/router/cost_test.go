package router

import (
	"strings"
	"testing"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int32
	}{
		{"", 0},
		{"a", 1},                            // floor of 1 even for sub-token strings
		{strings.Repeat("a", 4), 1},         // 4 chars / 4 = 1 token
		{strings.Repeat("a", 400), 100},     // proportional
		{strings.Repeat("a", 100_000), 25_000},
	}
	for _, c := range cases {
		got := EstimateTokens(c.in)
		if got != c.want {
			t.Errorf("EstimateTokens(len=%d) = %d, want %d", len(c.in), got, c.want)
		}
	}
}

func TestEstimateCost_ZeroPricingIsFree(t *testing.T) {
	got := EstimateCost("anything goes here", provider.Pricing{})
	if got != 0 {
		t.Errorf("expected zero cost for zero pricing, got %v", got)
	}
}

func TestEstimateCost_TracksWithPromptSize(t *testing.T) {
	p := provider.Pricing{InputPer1k: 0.001, OutputPer1k: 0.001}
	short := EstimateCost("hi", p)
	long := EstimateCost(strings.Repeat("x", 4_000), p)
	if !(long > short) {
		t.Errorf("expected longer prompt to cost more; short=%v long=%v", short, long)
	}
}
