package registry

import (
	"math"
	"testing"

	"astock_trade/core"
)

func TestGatedWeightOscillate(t *testing.T) {
	base := 2.0
	tests := []struct {
		name     string
		state    core.MarketState
		want     float64
	}{
		{"momentum", core.MarketOscillate, base * 0.67},
		{"reversal", core.MarketOscillate, base * 1.40},
		{"breakout", core.MarketOscillate, base * 0.75},
		{"volume", core.MarketOscillate, base * 1.33},
		{"volatility", core.MarketOscillate, base}, // no gating → 1.0
	}

	for _, tt := range tests {
		got := gatedWeight(tt.name, base, tt.state)
		if math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("gatedWeight(%q, %.1f, %s) = %.4f, want %.4f", tt.name, base, tt.state, got, tt.want)
		}
	}
}

func TestGatedWeightUptrend(t *testing.T) {
	base := 2.0
	if got := gatedWeight("reversal", base, core.MarketUptrend); math.Abs(got-base*0.5) > 1e-9 {
		t.Errorf("gatedWeight(reversal, %.1f, UPTREND) = %.4f, want %.4f", base, got, base*0.5)
	}
	if got := gatedWeight("momentum", base, core.MarketUptrend); math.Abs(got-base) > 1e-9 {
		t.Errorf("gatedWeight(momentum, %.1f, UPTREND) = %.4f, want %.4f", base, got, base)
	}
}

func TestGatedWeightDowntrend(t *testing.T) {
	base := 2.0
	if got := gatedWeight("momentum", base, core.MarketDowntrend); math.Abs(got-base*0.5) > 1e-9 {
		t.Errorf("gatedWeight(momentum, %.1f, DOWNTREND) = %.4f, want %.4f", base, got, base*0.5)
	}
	if got := gatedWeight("reversal", base, core.MarketDowntrend); math.Abs(got-base) > 1e-9 {
		t.Errorf("gatedWeight(reversal, %.1f, DOWNTREND) = %.4f, want %.4f", base, got, base)
	}
}

func TestGatedWeightOscillatePassesForOtherMarkets(t *testing.T) {
	// OSCILLATE multipliers should NOT affect UPTREND or DOWNTREND paths.
	base := 3.0
	// breakthrough in OSCILLATE would be base*0.75, but in UPTREND it must be base
	if got := gatedWeight("breakout", base, core.MarketUptrend); math.Abs(got-base) > 1e-9 {
		t.Errorf("gatedWeight(breakout, %.1f, UPTREND) = %.4f, want %.4f", base, got, base)
	}
	// volume in DOWNTREND must be base
	if got := gatedWeight("volume", base, core.MarketDowntrend); math.Abs(got-base) > 1e-9 {
		t.Errorf("gatedWeight(volume, %.1f, DOWNTREND) = %.4f, want %.4f", base, got, base)
	}
}
