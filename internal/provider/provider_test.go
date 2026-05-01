package provider

import (
	"math"
	"testing"
)

func TestPricing_Cost(t *testing.T) {
	cases := []struct {
		name    string
		pricing Pricing
		input   int32
		output  int32
		want    float64
	}{
		{
			name:    "zero rates always cost zero",
			pricing: Pricing{},
			input:   1000, output: 500,
			want: 0,
		},
		{
			name:    "input only",
			pricing: Pricing{InputPer1k: 0.01},
			input:   2000, output: 0,
			want: 0.02,
		},
		{
			name:    "output only",
			pricing: Pricing{OutputPer1k: 0.04},
			input:   0, output: 500,
			want: 0.02,
		},
		{
			name:    "mixed mirroring real provider rates",
			pricing: Pricing{InputPer1k: 0.005, OutputPer1k: 0.015},
			input:   1000, output: 1000,
			want: 0.020,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.pricing.Cost(c.input, c.output)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("expected %v, got %v", c.want, got)
			}
		})
	}
}
