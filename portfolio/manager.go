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

	// MinAllocation is the minimum CNY allocation to open a position.
	// Allocations below this are discarded to prevent absurdly small orders
	// where minimum commission (¥5) inflates the per-share fill price.
	// Default: 500.
	MinAllocation float64
}

// Manager satisfies core.PortfolioManager.
type Manager struct {
	mu     sync.RWMutex
	cfg    Config
	cash   float64       // actual cash, tracks realized PnL via OnTrade
	tiers  *EquityTiers  // nil means use legacy Config-based sizing
	equity float64       // current total equity (cash + market value), updated per tick
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
	if cfg.MinAllocation <= 0 {
		cfg.MinAllocation = 500
	}
	return &Manager{cfg: cfg, cash: cfg.TotalCapital}
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
// more position (by count and available cash).
func (m *Manager) CanOpenPosition(current []core.Position) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	maxPos := m.cfg.MaxPositions
	if m.tiers != nil {
		maxPos = m.tiers.MaxPositions(m.equity)
	}
	if len(current) >= maxPos {
		return false
	}
	if m.tiers != nil {
		return m.cash > 0
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

	if m.tiers == nil {
		return m.allocatePlanLegacy(current, maxRanks, result)
	}
	return m.allocatePlanTiers(current, maxRanks, result)
}

// allocatePlanLegacy uses the original Config-based sizing (TotalCapital, MaxSinglePct, MaxTotalPct).
func (m *Manager) allocatePlanLegacy(current []core.Position, maxRanks int, result []float64) []float64 {
	used := usedCapital(current)
	deployable := m.cfg.TotalCapital*m.cfg.MaxTotalPct - used
	if m.cash < deployable {
		deployable = m.cash
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
		raw := m.cfg.TotalCapital * pct
		capped := raw
		if capped > maxSingle {
			capped = maxSingle
		}
		actual := capped
		if actual > deployable {
			actual = deployable
		}
		if actual < m.cfg.MinAllocation {
			continue
		}
		result[i] = actual
		deployable -= actual
	}
	return result
}

// allocatePlanTiers uses equity-tier-based sizing: cash as deployable, absolute caps, dynamic MaxPositions.
func (m *Manager) allocatePlanTiers(current []core.Position, maxRanks int, result []float64) []float64 {
	// Deployable = actual cash (includes accumulated realized PnL).
	deployable := m.cash

	// Single-position cap from the current tier.
	maxSingle := m.tiers.SingleCap(m.equity)

	// Dynamic max positions for the current equity level.
	openSlots := m.tiers.MaxPositions(m.equity) - len(current)
	if openSlots < 0 {
		openSlots = 0
	}

	// Budget base grows with equity.
	budgetBase := m.equity
	if budgetBase <= 0 {
		budgetBase = m.cash
	}

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
		raw := budgetBase * pct // desired allocation grows with equity
		capped := raw
		if capped > maxSingle {
			capped = maxSingle // absolute single-position cap
		}
		actual := capped
		if actual > deployable {
			actual = deployable // remaining cash budget
		}
		if actual < m.cfg.MinAllocation {
			continue
		}
		result[i] = actual
		deployable -= actual
	}
	return result
}

// OnTrade updates the tracked cash balance after every confirmed trade.
func (m *Manager) OnTrade(side string, price float64, qty int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if side == "BUY" {
		m.cash -= price * float64(qty)
	} else {
		m.cash += price * float64(qty)
	}
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
	maxPos := m.cfg.MaxPositions
	if m.tiers != nil {
		maxPos = m.tiers.MaxPositions(m.equity)
		// When tiers are active, available = cash account balance.
		available = m.cash
		deployable = m.cash
	}
	return core.PortfolioStats{
		TotalCapital:     m.cfg.TotalCapital,
		UsedCapital:      used,
		AvailableCapital: available,
		DeployableCap:    deployable,
		UsedPct:          usedPct,
		PositionCount:    len(current),
		MaxPositions:     maxPos,
	}
}

// SetCash synchronises the tracked cash balance with an externally maintained
// value.  This is called on startup after the runtime state has been restored
// from the execution log; it MUST NOT be used for regular per-trade updates
// (those are handled by OnTrade).
func (m *Manager) SetCash(cash float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cash = cash
}

// SetTiers enables equity-tier-based dynamic sizing.  When nil the Manager
// falls back to the legacy Config-based sizing used by backtest / stress /
// validate entry points.
func (m *Manager) SetTiers(t *EquityTiers) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tiers = t
}

// UpdateEquity records the latest total equity so AllocatePlan, Stats etc.
// use the correct tier.  Called by the engine once per tick after computing
// equity = cash + Σ(position.Qty × marketPrice).
func (m *Manager) UpdateEquity(equity float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.equity = equity
}

// SetMaxTotalPct updates the maximum total-deployed-capital fraction at runtime.
// Implements core.MaxTotalPctSetter for adaptive position sizing.
func (m *Manager) SetMaxTotalPct(pct float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.MaxTotalPct = pct
}
