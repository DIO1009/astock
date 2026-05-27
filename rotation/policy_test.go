package rotation

import (
	"math"
	"testing"
	"time"

	"astock_trade/core"
)

func testPos() core.Position {
	return core.Position{Symbol: "600519", AvgPrice: 100, Quantity: 100, SellableQty: 100}
}

func testTime(clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", "2026-04-14 "+clock, time.FixedZone("CST", 8*3600))
	if err != nil {
		panic(err)
	}
	return t
}

func TestShouldRotateBlocksOpeningWindowAfterConfirmation(t *testing.T) {
	p := New(Config{RotationStartTime: "09:45", RotationConfirmTicks: 3})
	pos := testPos()
	old := RankInfo{Symbol: pos.Symbol, Rank: 92, Score: 0.08}
	newer := &CandidateInfo{Symbol: "300750", Rank: 5, Score: 0.30}

	for i := 0; i < 2; i++ {
		_ = p.ShouldRotate(pos, old, newer, testTime("09:40"), 1.0, 1)
	}
	dec := p.ShouldRotate(pos, old, newer, testTime("09:40"), 1.0, 1)
	if dec.Rotate || dec.Reason != BlockedOpeningWindow {
		t.Fatalf("expected opening-window block after confirmation, got rotate=%v reason=%s", dec.Rotate, dec.Reason)
	}
}

func TestShouldRotateBlocksRankBufferForTop55(t *testing.T) {
	p := New(Config{})
	dec := p.ShouldRotate(testPos(), RankInfo{Symbol: "600519", Rank: 55, Score: 0.10}, nil, testTime("10:00"), 0, 1)
	if dec.Rotate || dec.Reason != BlockedRankBuffer {
		t.Fatalf("expected rank-buffer block, got rotate=%v reason=%s", dec.Rotate, dec.Reason)
	}
}

func TestShouldRotateRequiresConsecutiveConfirmation(t *testing.T) {
	p := New(Config{RotationConfirmTicks: 3})
	pos := testPos()
	old := RankInfo{Symbol: pos.Symbol, Rank: 90, Score: 0.08}
	newer := &CandidateInfo{Symbol: "300750", Rank: 5, Score: 0.30}

	dec := p.ShouldRotate(pos, old, newer, testTime("10:00"), 0, 1)
	if dec.Rotate || dec.Reason != BlockedNotConfirmed {
		t.Fatalf("expected not-confirmed block, got rotate=%v reason=%s", dec.Rotate, dec.Reason)
	}
	_ = p.ShouldRotate(pos, old, newer, testTime("10:00"), 0, 1)
	dec = p.ShouldRotate(pos, old, newer, testTime("10:00"), 0, 1)
	if !dec.Rotate || dec.Reason != Confirmed {
		t.Fatalf("expected confirmed rotation on third lag tick, got rotate=%v reason=%s", dec.Rotate, dec.Reason)
	}
}

func TestShouldRotateRequiresBetterCandidateDelta(t *testing.T) {
	p := New(Config{RotationConfirmTicks: 1, RotationDelta: 0.10})
	dec := p.ShouldRotate(
		testPos(),
		RankInfo{Symbol: "600519", Rank: 90, Score: 0.08},
		&CandidateInfo{Symbol: "300750", Rank: 5, Score: 0.15},
		testTime("10:00"),
		0,
		1,
	)
	if dec.Rotate || dec.Reason != BlockedDeltaTooSmall {
		t.Fatalf("expected delta-too-small block, got rotate=%v reason=%s delta=%f", dec.Rotate, dec.Reason, dec.ScoreDelta)
	}
}

func TestShouldRotateRaisesDeltaForLosingPosition(t *testing.T) {
	p := New(Config{RotationConfirmTicks: 1, RotationDelta: 0.10, LossDeltaMultiplier: 1.5})
	dec := p.ShouldRotate(
		testPos(),
		RankInfo{Symbol: "600519", Rank: 90, Score: 0.08},
		&CandidateInfo{Symbol: "300750", Rank: 5, Score: 0.21},
		testTime("10:00"),
		-2.3,
		1,
	)
	if dec.Rotate || dec.Reason != BlockedDeltaTooSmall || math.Abs(dec.EffectiveDelta-0.15) > 1e-9 {
		t.Fatalf("expected stricter loss delta block, got rotate=%v reason=%s effective=%f", dec.Rotate, dec.Reason, dec.EffectiveDelta)
	}
}

func TestShouldRotateEnforcesDailyLimit(t *testing.T) {
	p := New(Config{RotationConfirmTicks: 1, MaxRotationPerDay: 1})
	pos := testPos()
	old := RankInfo{Symbol: pos.Symbol, Rank: 90, Score: 0.08}
	newer := &CandidateInfo{Symbol: "300750", Rank: 5, Score: 0.30}

	dec := p.ShouldRotate(pos, old, newer, testTime("10:00"), 0, 1)
	if !dec.Rotate {
		t.Fatalf("expected first rotation allowed, reason=%s", dec.Reason)
	}
	p.RecordRotation(1)
	dec = p.ShouldRotate(pos, old, newer, testTime("10:00"), 0, 1)
	if dec.Rotate || dec.Reason != BlockedDailyLimit {
		t.Fatalf("expected daily-limit block, got rotate=%v reason=%s", dec.Rotate, dec.Reason)
	}
}
