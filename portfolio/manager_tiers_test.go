package portfolio

import (
	"math"
	"testing"

	"astock_trade/core"
)

func makeValidTiers() *EquityTiers {
	return &EquityTiers{
		BasePositions: 10,
		Tiers: []EquityTier{
			{MinEquity: 100000, SinglePositionCap: 20000},
			{MinEquity: 200000, SinglePositionCap: 40000},
			{MinEquity: 300000, SinglePositionCap: 60000},
		},
	}
}

func TestAllocatePlan_Tiers_Equity100k(t *testing.T) {
	tiers := makeValidTiers()
	m := New(Config{
		TotalCapital: 100000,
		MaxPositions: 3,
		RankPcts:     []float64{0.15, 0.15, 0.07},
		MinAllocation: 500,
	})
	m.SetTiers(tiers)
	m.UpdateEquity(100000)

	// 权益 10 万：单仓上限 20,000，最大持仓 10
	// 可用资金 = cash = 100000
	alloc := m.AllocatePlan(nil, 3)

	// Rank#1: 15% × 100k = 15k, capped by 20k → 15k
	if alloc[0] < 14900 || alloc[0] > 15100 {
		t.Errorf("rank#1 = %.0f, want ~15000", alloc[0])
	}
	// Rank#2: 15% × 100k = 15k, capped by 20k → 15k
	if alloc[1] < 14900 || alloc[1] > 15100 {
		t.Errorf("rank#2 = %.0f, want ~15000", alloc[1])
	}
	// Rank#3: 7% × 100k = 7k, capped by 20k → 7k
	if alloc[2] < 6900 || alloc[2] > 7100 {
		t.Errorf("rank#3 = %.0f, want ~7000", alloc[2])
	}
}

func TestAllocatePlan_Tiers_BudgetGrowsWithEquity(t *testing.T) {
	tiers := makeValidTiers()
	m := New(Config{
		TotalCapital: 100000,
		MaxPositions: 3,
		RankPcts:     []float64{0.15, 0.15, 0.07},
		MinAllocation: 500,
	})
	m.SetTiers(tiers)
	m.UpdateEquity(120000)

	// 权益 12 万：单仓上限仍 20,000，最大持仓 11
	// Rank#1: 15% × 120k = 18k
	alloc := m.AllocatePlan(nil, 3)

	if alloc[0] < 17900 || alloc[0] > 18100 {
		t.Errorf("rank#1 = %.0f, want ~18000", alloc[0])
	}
}

func TestAllocatePlan_Tiers_CappedBySingleLimit(t *testing.T) {
	tiers := makeValidTiers()
	m := New(Config{
		TotalCapital: 100000,
		MaxPositions: 3,
		RankPcts:     []float64{0.15, 0.15, 0.07},
		MinAllocation: 500,
	})
	m.SetTiers(tiers)
	m.UpdateEquity(140000)

	// 权益 14 万：单仓上限 20,000
	// Rank#1: 15% × 140k = 21k, capped by 20k → 20k
	alloc := m.AllocatePlan(nil, 3)

	if math.Abs(alloc[0]-20000) > 1 {
		t.Errorf("rank#1 = %.0f, want 20000 (capped)", alloc[0])
	}
}

func TestAllocatePlan_Tiers_Equity200k(t *testing.T) {
	tiers := makeValidTiers()
	m := New(Config{
		TotalCapital: 100000,
		MaxPositions: 3,
		RankPcts:     []float64{0.15, 0.15, 0.07},
		MinAllocation: 500,
	})
	m.SetTiers(tiers)
	m.UpdateEquity(200000)

	// 权益 20 万：单仓上限 40,000，最大持仓重置回 10
	// Rank#1: 15% × 200k = 30k, capped by 40k → 30k
	alloc := m.AllocatePlan(nil, 3)

	if alloc[0] < 29900 || alloc[0] > 30100 {
		t.Errorf("rank#1 = %.0f, want ~30000", alloc[0])
	}
}

func TestAllocatePlan_Tiers_OpenSlotsLimited(t *testing.T) {
	tiers := makeValidTiers()
	m := New(Config{
		TotalCapital: 100000,
		MaxPositions: 3,
		RankPcts:     []float64{0.15, 0.15, 0.07},
		MinAllocation: 500,
	})
	m.SetTiers(tiers)
	m.UpdateEquity(100000)

	// 权益 10 万：最大持仓 10
	// 已有 9 个持仓 → 只剩 1 个空位
	existing := makePositions(9)
	alloc := m.AllocatePlan(existing, 5)

	// Only rank#0 should get an allocation
	if alloc[0] <= 0 {
		t.Error("rank#0 should have allocation")
	}
	if alloc[1] > 0 {
		t.Error("rank#1 should be 0 (no open slots)")
	}
}

func TestAllocatePlan_Tiers_UsingCashAsDeployable(t *testing.T) {
	tiers := makeValidTiers()
	m := New(Config{
		TotalCapital: 200000,
		MaxPositions: 3,
		RankPcts:     []float64{0.15, 0.15, 0.07},
		MinAllocation: 500,
	})
	m.SetTiers(tiers)
	// Simulate a loss: initial capital 100k, accumulated losses → cash = 90000
	m.SetCash(90000)
	m.UpdateEquity(90000)

	alloc := m.AllocatePlan(nil, 3)

	// deployable should be cash = 90000, not TotalCapital-based
	totalAlloc := alloc[0] + alloc[1] + alloc[2]
	if totalAlloc > 90000 {
		t.Errorf("total allocation %.0f > cash 90000", totalAlloc)
	}
}

func TestAllocatePlan_Tiers_MaxPositionsDynamic(t *testing.T) {
	tiers := makeValidTiers()
	m := New(Config{
		TotalCapital: 100000,
		MaxPositions: 3,
		RankPcts:     []float64{0.15, 0.15, 0.07},
		MinAllocation: 500,
	})
	m.SetTiers(tiers)

	// Equity 100k → maxPositions = 10
	m.UpdateEquity(100000)
	if ok := m.CanOpenPosition(makePositions(9)); !ok {
		t.Error("equity=100k: should be able to open 10th position")
	}
	if ok := m.CanOpenPosition(makePositions(10)); ok {
		t.Error("equity=100k: should NOT be able to open 11th position")
	}

	// Equity 120k → maxPositions = 11 (=10 + floor(20k/20k))
	m.UpdateEquity(120000)
	if ok := m.CanOpenPosition(makePositions(10)); !ok {
		t.Error("equity=120k: should be able to open 11th position")
	}

	// Equity 200k → maxPositions resets to 10
	m.UpdateEquity(200000)
	if ok := m.CanOpenPosition(makePositions(9)); !ok {
		t.Error("equity=200k: should be able to open 10th position")
	}
	if ok := m.CanOpenPosition(makePositions(10)); ok {
		t.Error("equity=200k: should NOT be able to open 11th position")
	}

	// Equity 240k → maxPositions = 11 (=10 + floor(40k/40k))
	m.UpdateEquity(240000)
	if ok := m.CanOpenPosition(makePositions(10)); !ok {
		t.Error("equity=240k: should be able to open 11th position")
	}
	if ok := m.CanOpenPosition(makePositions(11)); ok {
		t.Error("equity=240k: should NOT be able to open 12th position")
	}
}

func TestAllocatePlan_Legacy_StillWorks(t *testing.T) {
	// Without tiers, legacy behavior must be preserved.
	m := New(Config{
		TotalCapital: 100000,
		MaxPositions: 5,
		MaxSinglePct: 0.30,
		MaxTotalPct:  0.80,
		RankPcts:     []float64{0.25, 0.25, 0.20, 0.15, 0.15},
		MinAllocation: 500,
	})

	alloc := m.AllocatePlan(nil, 3)

	// Rank#1: total=100k×0.25=25k, capped by MaxSinglePct=30k → 25k
	// But deployable = 100k×0.8 = 80k, so all fit
	if alloc[0] < 24900 || alloc[0] > 25100 {
		t.Errorf("legacy rank#1 = %.0f, want ~25000", alloc[0])
	}
	if alloc[1] < 24900 || alloc[1] > 25100 {
		t.Errorf("legacy rank#2 = %.0f, want ~25000", alloc[1])
	}
	if alloc[2] < 19900 || alloc[2] > 20100 {
		t.Errorf("legacy rank#3 = %.0f, want ~20000", alloc[2])
	}

	// MaxPositions = 5, already have 4 → only 1 slot left
	m2 := New(Config{
		TotalCapital: 100000,
		MaxPositions: 5,
		MaxSinglePct: 0.30,
		MaxTotalPct:  0.80,
		RankPcts:     []float64{0.25, 0.25, 0.20},
		MinAllocation: 500,
	})
	alloc2 := m2.AllocatePlan(makePositions(4), 3)
	if alloc2[0] <= 0 {
		t.Error("legacy: rank#0 should have allocation with 1 slot left")
	}
	if alloc2[1] > 0 {
		t.Error("legacy: rank#1 should be 0 (only 1 slot left)")
	}
}

// Helper

func makePositions(n int) []core.Position {
	pos := make([]core.Position, n)
	for i := 0; i < n; i++ {
		pos[i] = core.Position{
			Symbol:   string(rune('A' + i)),
			AvgPrice: 10,
			Quantity: 100,
		}
	}
	return pos
}
