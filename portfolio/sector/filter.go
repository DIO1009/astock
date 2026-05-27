// Package sector provides SectorDecision: a core.PortfolioDecision wrapper that
// enforces sector-level concentration limits on top of any inner decision maker.
//
// Motivation:
// Without sector constraints a portfolio of 8 positions could end up 100% in
// TECH during a growth rally, wiping out diversification benefits.  This wrapper
// filters out BUY orders that would push any sector above MaxSectorPct of total
// capital, ensuring the portfolio remains sector-diversified.
//
// Usage:
//
//	inner := topn.New(topn.Config{…})
//	decision := sector.NewDecision(inner, sector.Config{
//	    SectorOf:     universe.SectorOf,
//	    MaxSectorPct: 0.40,          // no sector may exceed 40% of capital
//	    TotalCapital: 100_000,
//	})
//	// pass decision to engine.New(…)
package sector

import (
	"log"

	"astock_trade/core"
)

// Config configures the SectorDecision filter.
type Config struct {
	// SectorOf maps symbol → sector name (e.g. "TECH", "FINANCE").
	// Symbols absent from this map are treated as sector "UNKNOWN" and are
	// always allowed.
	SectorOf map[string]string

	// MaxSectorPct is the maximum fraction of TotalCapital that may be
	// invested in any single sector.  Example: 0.40 = 40%.
	// 0 or negative disables the sector check.
	MaxSectorPct float64

	// TotalCapital is the account size used to compute sector exposure %.
	TotalCapital float64
}

// Decision wraps core.PortfolioDecision and adds sector-exposure filtering.
// It satisfies core.PortfolioDecision and optionally core.BuyThresholdSetter.
type Decision struct {
	inner core.PortfolioDecision
	cfg   Config
}

// NewDecision returns a SectorDecision.
func NewDecision(inner core.PortfolioDecision, cfg Config) *Decision {
	return &Decision{inner: inner, cfg: cfg}
}

// Decide delegates to the inner decision maker and then removes any BUY orders
// that would violate sector concentration limits.
func (d *Decision) Decide(
	buySignals []core.Signal,
	quotes map[string]*core.Quote,
	positions []core.Position,
	allocations []float64,
) []core.Order {
	orders := d.inner.Decide(buySignals, quotes, positions, allocations)

	if d.cfg.MaxSectorPct <= 0 || d.cfg.TotalCapital <= 0 {
		return orders
	}

	// Compute current sector exposure from open positions.
	sectorUsed := make(map[string]float64)
	for _, pos := range positions {
		sec := d.sectorOf(pos.Symbol)
		sectorUsed[sec] += pos.AvgPrice * float64(pos.Quantity)
	}

	// Filter BUY orders that would breach the sector cap.
	result := make([]core.Order, 0, len(orders))
	for _, order := range orders {
		if order.Side != "BUY" {
			result = append(result, order)
			continue
		}
		sec := d.sectorOf(order.Symbol)
		if sec == "UNKNOWN" {
			result = append(result, order)
			continue
		}
		orderCost := float64(order.Quantity) * order.Price
		if (sectorUsed[sec]+orderCost)/d.cfg.TotalCapital > d.cfg.MaxSectorPct {
			log.Printf("  🏭 [SectorFilter] BUY %-8s blocked – sector=%-12s exposure=%.1f%%/%.0f%%",
				order.Symbol, sec,
				(sectorUsed[sec]+orderCost)/d.cfg.TotalCapital*100,
				d.cfg.MaxSectorPct*100)
			continue
		}
		sectorUsed[sec] += orderCost
		result = append(result, order)
	}
	return result
}

// SetBuyThreshold delegates to the inner decision if it implements
// core.BuyThresholdSetter (e.g. topn.Decision).
func (d *Decision) SetBuyThreshold(threshold float64) {
	if setter, ok := d.inner.(core.BuyThresholdSetter); ok {
		setter.SetBuyThreshold(threshold)
	}
}

func (d *Decision) sectorOf(symbol string) string {
	if d.cfg.SectorOf == nil {
		return "UNKNOWN"
	}
	if sec, ok := d.cfg.SectorOf[symbol]; ok {
		return sec
	}
	return "UNKNOWN"
}

// Compile-time interface checks.
var (
	_ core.PortfolioDecision = (*Decision)(nil)
	_ core.BuyThresholdSetter = (*Decision)(nil)
)
