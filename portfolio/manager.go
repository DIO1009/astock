// Package portfolio implements capital allocation with hard position-count,
// single-position, and total-deployed-capital limits.
//
// Capital flow per tick:
//
//	TotalCapital (fixed)
//	  └── UsedCapital  = Σ(AvgPrice × Qty)      ← tracked via positions
//	  └── AvailableCap = TotalCapital − UsedCap  ← raw cash on hand
//	  └── DeployableCap = TotalCapital×MaxTotalPct − UsedCap  ← respects 80% ceiling
//
// AllocatePlan distributes DeployableCap across ranks using RankPcts, subject
// to the per-position MaxSinglePct ceiling.  Each slot is computed greedily:
//
//	slot_i = min(TotalCapital×RankPcts[i], TotalCapital×MaxSinglePct, remaining_deployable)
//
// Thread-safe via sync.RWMutex.
package portfolio

import (
	"sync"

	"astock_trade/core"
)

// Config holds all portfolio-level parameters.
type Config struct {
	// TotalCapital is the total simulated account equity in CNY.
	// Example: 100_000
	TotalCapital float64

	// MaxPositions is the hard cap on concurrent holdings.
	// Example: 3
	MaxPositions int

	// MaxSinglePct caps any one position as a fraction of TotalCapital.
	// Example: 0.30 → max ¥30,000 per stock on a ¥100,000 account.
	MaxSinglePct float64

	// MaxTotalPct caps total deployed capital as a fraction of TotalCapital.
	// The remainder is kept as a cash buffer for risk management.
	// Example: 0.80 → max ¥80,000 deployed; ¥20,000 always reserved.
	MaxTotalPct float64

	// RankPcts defines the desired capital fraction for each rank slot.
	// RankPcts[0] = fraction for rank#1 (highest-scoring signal), etc.
	// Fractions are applied to TotalCapital, then capped by MaxSinglePct and
	// residual DeployableCap.
	// Example: [0.40, 0.30, 0.30] → rank#1 wants 40%, rank#2 30%, rank#3 30%.
	RankPcts []float64
}

// Manager satisfies core.PortfolioManager.
type Manager struct {
	mu  sync.RWMutex
	cfg Config
}

// New returns a Manager.  Sensible defaults are applied for any zero values.
func New(cfg Config) *Manager {
	if cfg.MaxTotalPct <= 0 {
		cfg.MaxTotalPct = 0.80
	}
	if cfg.MaxSinglePct <= 0 {
		cfg.MaxSinglePct = 0.30
	}
	if cfg.MaxPositions <= 0 {
		cfg.MaxPositions = 3
	}
	if len(cfg.RankPcts) == 0 {
		// Equal-weight fallback.
		each := 1.0 / float64(cfg.MaxPositions)
		cfg.RankPcts = make([]float64, cfg.MaxPositions)
		for i := range cfg.RankPcts {
			cfg.RankPcts[i] = each
		}
	}
	return &Manager{cfg: cfg}
}

// usedCapital computes cost basis of all open positions.
func usedCapital(positions []core.Position) float64 {
	total := 0.0
	for _, p := range positions {
		total += p.AvgPrice * float64(p.Quantity)
	}
	return total
}

// CanOpenPosition returns true when the portfolio can accept at least one
// more position (by count, cash, and total-pct ceiling).
func (m *Manager) CanOpenPosition(current []core.Position) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(current) >= m.cfg.MaxPositions {
		return false
	}
	used := usedCapital(current)
	deployable := m.cfg.TotalCapital*m.cfg.MaxTotalPct - used
	return deployable > 0
}

// AllocatePlan computes the per-rank CNY allocation for up to maxRanks BUY slots.
//
// For each rank i:
//  1. raw   = TotalCapital × RankPcts[i]        (desired amount)
//  2. capped = min(raw, TotalCapital × MaxSinglePct)   (per-stock limit)
//  3. actual = min(capped, remaining_deployable)        (total-deployed limit)
//
// Open slots are also respected: if len(current) + i >= MaxPositions the slot
// returns 0.
func (m *Manager) AllocatePlan(current []core.Position, maxRanks int) []float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]float64, maxRanks)

	used := usedCapital(current)
	deployable := m.cfg.TotalCapital*m.cfg.MaxTotalPct - used
	if deployable <= 0 {
		return result // all zeros
	}

	maxSingle := m.cfg.TotalCapital * m.cfg.MaxSinglePct
	openSlots := m.cfg.MaxPositions - len(current)

	for i := 0; i < maxRanks; i++ {
		if i >= openSlots || deployable <= 0 {
			break
		}
		pct := 0.0
		if i < len(m.cfg.RankPcts) {
			pct = m.cfg.RankPcts[i]
		}
		if pct <= 0 {
			continue
		}
		raw := m.cfg.TotalCapital * pct // desired
		capped := raw
		if capped > maxSingle {
			capped = maxSingle // per-stock cap
		}
		actual := capped
		if actual > deployable {
			actual = deployable // remaining deployable budget
		}
		result[i] = actual
		deployable -= actual
	}
	return result
}

// Stats returns a snapshot of current portfolio metrics.
func (m *Manager) Stats(current []core.Position) core.PortfolioStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	used := usedCapital(current)
	available := m.cfg.TotalCapital - used
	deployable := m.cfg.TotalCapital*m.cfg.MaxTotalPct - used
	if deployable < 0 {
		deployable = 0
	}
	usedPct := 0.0
	if m.cfg.TotalCapital > 0 {
		usedPct = used / m.cfg.TotalCapital * 100
	}
	return core.PortfolioStats{
		TotalCapital:     m.cfg.TotalCapital,
		UsedCapital:      used,
		AvailableCapital: available,
		DeployableCap:    deployable,
		UsedPct:          usedPct,
		PositionCount:    len(current),
		MaxPositions:     m.cfg.MaxPositions,
	}
}

// SetMaxTotalPct updates the maximum total-deployed-capital fraction at runtime.
// Implements core.MaxTotalPctSetter for adaptive position sizing.
func (m *Manager) SetMaxTotalPct(pct float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.MaxTotalPct = pct
}
