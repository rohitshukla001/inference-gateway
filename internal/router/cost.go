package router

import "github.com/rshuklas/inference-gateway/internal/provider"

const (
	// charsPerToken is the conventional approximation used for pre-flight
	// token estimates without running a real tokenizer.
	charsPerToken = 4

	// MaxOutputTokens is the completion cap sent to providers and the
	// worst-case length assumed when projecting cost for the budget gate, so
	// a call can never cost more than its projection on output.
	MaxOutputTokens = 512
)

// EstimateTokens approximates token count from raw character length. It is
// used for pre-flight budget gates only; actual billing uses the token
// counts returned by the provider.
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

// EstimateCost returns a worst-case projection for the given prompt against
// the supplied pricing, assuming the model emits up to
// MaxOutputTokens of output. Used to gate requests against a remaining
// budget; deliberately conservative.
func EstimateCost(prompt string, pricing provider.Pricing) float64 {
	return pricing.Cost(EstimateTokens(prompt), MaxOutputTokens)
}
