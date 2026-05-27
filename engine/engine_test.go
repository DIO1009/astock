package engine

import (
	"testing"

	"astock_trade/core"
)

func TestRegimeMinScoreOscillateUsesConfiguredFloor(t *testing.T) {
	e := &Engine{}
	signals := []core.Signal{
		{Score: 0.10},
		{Score: 0.20},
		{Score: 0.25},
	}

	minScore, source, _ := e.regimeMinScore(signals, core.MarketOscillate, 0.30)
	if minScore != 0.30 {
		t.Fatalf("minScore = %.2f, want 0.30", minScore)
	}
	if source != "max(p90(total_score),config_floor)" {
		t.Fatalf("source = %q, want config floor source", source)
	}
}

func TestRegimeMinScoreOscillateKeepsHigherP90(t *testing.T) {
	e := &Engine{}
	signals := []core.Signal{
		{Score: 0.10},
		{Score: 0.35},
		{Score: 0.50},
	}

	minScore, source, _ := e.regimeMinScore(signals, core.MarketOscillate, 0.30)
	if minScore <= 0.30 {
		t.Fatalf("minScore = %.2f, want above configured floor", minScore)
	}
	if source != "p90(total_score)" {
		t.Fatalf("source = %q, want p90 source", source)
	}
}
