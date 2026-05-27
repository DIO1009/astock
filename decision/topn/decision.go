// Package topn provides a PortfolioDecision that generates BUY orders ONLY.
//
// SELL is exclusively handled by PositionManager.CheckExit (stop-loss /
// take-profit / trailing stop).
//
// Capital sizing comes entirely from the pre-computed allocations slice
// supplied by PortfolioManager.AllocatePlan – topn has no sizing math of
// its own and carries no TotalCapital reference.
//
// Pipeline per tick:
//  1. For each rank i (0-indexed, already sorted descending by AlphaEngine):
//     a. Skip if i ≥ TopN or allocations[i] == 0 (no budget).
//     b. Skip if score < BuyThreshold (quality gate).
//     c. Skip if already holding the symbol.
//     d. Compute qty = allocations[i] / ask1; skip if qty < 1.
//     e. Emit BUY order with reason string for audit log.
package topn

import (
	"fmt"

	"astock_trade/core"
)

// Config holds decision parameters.
type Config struct {
	// MaxPositions caps the total number of concurrent holdings.
	// Used to compute openSlots = MaxPositions - len(positions).
	MaxPositions int

	// TopN is a secondary rank-cap applied on the already-stable buy candidates.
	// Example: TopN=3 → even if 5 stable signals arrive, only the top 3 qualify.
	TopN int

	// BuyThreshold: minimum score a signal must reach to trigger BUY.
	// Sorted list allows safe break when threshold is not met.
	BuyThreshold float64
}

// Decision satisfies core.PortfolioDecision.
type Decision struct {
	cfg Config
}

// New returns a Decision.
func New(cfg Config) *Decision {
	if cfg.TopN <= 0 {
		cfg.TopN = cfg.MaxPositions
	}
	return &Decision{cfg: cfg}
}

// Decide generates BUY orders for signals that pass all guards.
//
// allocations[i] is the exact CNY budget for rank i (pre-computed by
// PortfolioManager.AllocatePlan).  A zero value means "no budget → skip."
func (d *Decision) Decide(
	buySignals  []core.Signal,
	quotes      map[string]*core.Quote,
	positions   []core.Position,
	allocations []float64,
) []core.Order {
	orders := make([]core.Order, 0, len(buySignals))

	held := make(map[string]bool, len(positions))
	for _, p := range positions {
		held[p.Symbol] = true
	}

	openSlots := d.cfg.MaxPositions - len(positions)
	if openSlots <= 0 {
		return orders
	}

	buysPlaced := 0
	for i, sig := range buySignals {
		if i >= d.cfg.TopN {
			break
		}
		if buysPlaced >= openSlots {
			break
		}
		// BuyThreshold quality gate (list sorted → safe to break).
		if sig.Score < d.cfg.BuyThreshold {
			break
		}
		// Budget gate: PortfolioManager returned 0 for this rank.
		alloc := 0.0
		if i < len(allocations) {
			alloc = allocations[i]
		}
		if alloc <= 0 {
			continue
		}
		if held[sig.Symbol] {
			continue // already holding; no duplicate BUY
		}

		q, qok := quotes[sig.Symbol]
		if !qok || q.Ask1 <= 0 {
			continue
		}

		qty := int(alloc / q.Ask1)
		if qty <= 0 {
			continue
		}

		reason := fmt.Sprintf(
			"ALPHA  rank#%d  score=%+.4f  alloc=¥%.0f",
			i+1, sig.Score, alloc,
		)
		orders = append(orders, core.Order{
			Symbol:   sig.Symbol,
			Side:     "BUY",
			Price:    q.Ask1,
			Quantity: qty,
			Reason:   reason,
		})
		buysPlaced++
	}

	return orders
}

// SetBuyThreshold updates the minimum alpha score required to trigger a BUY at runtime.
// Implements core.BuyThresholdSetter for adaptive entry-quality control.
func (d *Decision) SetBuyThreshold(threshold float64) {
	d.cfg.BuyThreshold = threshold
}
