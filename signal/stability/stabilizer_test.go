package stability

import (
	"testing"

	"astock_trade/core"
)

// signals helper returns simple signals for testing.
func sigs(symbols ...string) []core.Signal {
	out := make([]core.Signal, len(symbols))
	for i, s := range symbols {
		out[i] = core.Signal{Symbol: s, Score: float64(len(symbols) - i)}
	}
	return out
}

func TestStabilizeOscillateRequiresExtraTick(t *testing.T) {
	s := New(Config{TopN: 2, MinConsecutive: 2})
	s.SetMarketState(core.MarketOscillate)

	// Tick 1: symbols appear in TopN
	stable, _ := s.Stabilize(sigs("A", "B", "C"))
	if len(stable) != 0 {
		t.Fatalf("Tick 1: expected 0 stable, got %d (effectiveMin=3, counts=1)", len(stable))
	}

	// Tick 2
	stable, _ = s.Stabilize(sigs("A", "B", "C"))
	if len(stable) != 0 {
		t.Fatalf("Tick 2: expected 0 stable, got %d (effectiveMin=3, counts=2)", len(stable))
	}

	// Tick 3: counts reach 3 → stable
	stable, _ = s.Stabilize(sigs("A", "B", "C"))
	if len(stable) != 2 {
		t.Fatalf("Tick 3: expected 2 stable, got %d (effectiveMin=3, counts=3)", len(stable))
	}
}

func TestStabilizeUptrendUsesOriginalMinConsecutive(t *testing.T) {
	s := New(Config{TopN: 2, MinConsecutive: 2})
	s.SetMarketState(core.MarketUptrend)

	// Tick 1
	stable, _ := s.Stabilize(sigs("A", "B", "C"))
	if len(stable) != 0 {
		t.Fatalf("Tick 1 UPTREND: expected 0 stable (counts=1, min=2), got %d", len(stable))
	}

	// Tick 2: counts=2, min=2 → stable
	stable, _ = s.Stabilize(sigs("A", "B", "C"))
	if len(stable) != 2 {
		t.Fatalf("Tick 2 UPTREND: expected 2 stable, got %d", len(stable))
	}
}

func TestStabilizeDowntrendUsesOriginalMinConsecutive(t *testing.T) {
	s := New(Config{TopN: 2, MinConsecutive: 2})
	s.SetMarketState(core.MarketDowntrend)

	// Tick 1
	stable, _ := s.Stabilize(sigs("A", "B", "C"))
	if len(stable) != 0 {
		t.Fatalf("Tick 1 DOWNTREND: expected 0 stable, got %d", len(stable))
	}

	// Tick 2
	stable, _ = s.Stabilize(sigs("A", "B", "C"))
	if len(stable) != 2 {
		t.Fatalf("Tick 2 DOWNTREND: expected 2 stable, got %d", len(stable))
	}
}

func TestStabilizeSetMarketStateAfterCountsKeepsCounts(t *testing.T) {
	s := New(Config{TopN: 2, MinConsecutive: 2})

	// Two ticks in OSCILLATE → counts=2, not yet stable (effectiveMin=3)
	s.SetMarketState(core.MarketOscillate)
	s.Stabilize(sigs("A", "B", "C"))
	s.Stabilize(sigs("A", "B", "C"))

	// Switch to UPTREND → counts preserved, effectiveMin=2 now
	s.SetMarketState(core.MarketUptrend)
	stable, _ := s.Stabilize(sigs("A", "B", "C"))
	if len(stable) != 2 {
		t.Fatalf("After switching to UPTREND with counts=3: expected 2 stable, got %d", len(stable))
	}
}

func TestStabilizeDefaultMarketStateIsOscillate(t *testing.T) {
	s := New(Config{TopN: 2, MinConsecutive: 2})
	// Default marketState = MarketOscillate → effectiveMin = 3
	stable, _ := s.Stabilize(sigs("A", "B", "C"))
	if len(stable) != 0 {
		t.Fatalf("Default state OSCILLATE tick 1: expected 0 stable, got %d", len(stable))
	}
}
