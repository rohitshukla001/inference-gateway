package router

import "github.com/rshuklas/inference-gateway/internal/provider"

const (
	// charsPerToken approximates tokens without running a tokenizer.
	charsPerToken = 4

	// MaxOutputTokens is the completion cap sent to providers and the output
	// length assumed when projecting cost, so output cost never exceeds the
	// projection.
	MaxOutputTokens = 512
)

// EstimateTokens approximates token count from character length. Only cost
// projections use it; billing uses provider-reported counts.
func EstimateTokens(text string) int32 {
	if text == "" {
		return 0
	}
	n := len(text) / charsPerToken
	if n < 1 {
		return 1
	}
	return int32(n)
}

// EstimateCost returns a worst-case cost for prompt, assuming MaxOutputTokens
// of output. The budget gate and the max_cost filter use it.
func EstimateCost(prompt string, pricing provider.Pricing) float64 {
	return pricing.Cost(EstimateTokens(prompt), MaxOutputTokens)
}
