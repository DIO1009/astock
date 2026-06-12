package engine

import (
	"math"
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

func TestRegimeMinScoreOscillateAppliesThresholdMultiplier(t *testing.T) {
	e := &Engine{cfg: Config{OscillateThresholdMult: 1.3}}
	signals := []core.Signal{
		{Score: 0.10},
		{Score: 0.20},
		{Score: 0.25},
	}

	minScore, source, _ := e.regimeMinScore(signals, core.MarketOscillate, 0.30)
	want := 0.30 * 1.3
	if math.Abs(minScore-want) > 1e-9 {
		t.Fatalf("minScore = %.4f, want %.4f", minScore, want)
	}
	if source != "max(p90(total_score),config_floor)×1.30" {
		t.Fatalf("source = %q, want multiplier source", source)
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
