// Package adaptive provides an AdaptiveOptimizer that adjusts trading parameters
// in real-time based on the system's current performance metrics.
//
// Rules (applied in priority order):
//
//  1. DrawdownGuard: if MaxDrawdown > DrawdownThreshold → reduce position size
//     (MaxTotalPct is lowered to ReducedMaxTotalPct until drawdown recovers).
//
//  2. WinRateGuard: if WinRate < WinRateThreshold AND TradeCount ≥ MinTrades
//     → raise entry threshold (BuyThreshold is raised to RaisedBuyThreshold).
//
// Each rule reverts to its normal value when the condition is no longer met.
//
// The optimizer is stateless between calls to Params(); it derives its output
// purely from the supplied PerformanceReport.
package adaptive

import (
	"fmt"

	"astock_trade/core"
)

// Config holds all tunable thresholds for the adaptive rules.
type Config struct {
	// DrawdownThreshold: MaxDrawdown (%) above which position size is reduced.
	// Default 8.0 (%).
	DrawdownThreshold float64

	// WinRateThreshold: WinRate (%) below which BuyThreshold is raised.
	// Default 35.0 (%).
	WinRateThreshold float64

	// MinTrades: minimum number of closed trades before WinRateGuard activates.
	// Prevents over-reaction to small samples.  Default 5.
	MinTrades int

	// NormalMaxTotalPct: base capital deployment ratio (0–1).  Default 0.80.
	NormalMaxTotalPct float64

	// ReducedMaxTotalPct: deployment ratio when DrawdownGuard fires.  Default 0.50.
	ReducedMaxTotalPct float64

	// NormalBuyThreshold: base minimum alpha score for BUY.  Default 0.08.
	NormalBuyThreshold float64

	// RaisedBuyThreshold: BuyThreshold when WinRateGuard fires.  Default 0.15.
	RaisedBuyThreshold float64
}

// Optimizer satisfies core.AdaptiveOptimizer.
type Optimizer struct {
	cfg Config
}

// New returns an Optimizer with defaults applied for zero-value fields.
func New(cfg Config) *Optimizer {
	if cfg.DrawdownThreshold <= 0 {
		cfg.DrawdownThreshold = 8.0
	}
	if cfg.WinRateThreshold <= 0 {
		cfg.WinRateThreshold = 35.0
	}
	if cfg.MinTrades <= 0 {
		cfg.MinTrades = 5
	}
	if cfg.NormalMaxTotalPct <= 0 {
		cfg.NormalMaxTotalPct = 0.80
	}
	if cfg.ReducedMaxTotalPct <= 0 {
		cfg.ReducedMaxTotalPct = 0.50
	}
	if cfg.NormalBuyThreshold <= 0 {
		cfg.NormalBuyThreshold = 0.08
	}
	if cfg.RaisedBuyThreshold <= 0 {
		cfg.RaisedBuyThreshold = 0.15
	}
	return &Optimizer{cfg: cfg}
}

// Params derives recommended trading parameters from the current performance report.
// It is safe to call every tick; the computation is O(1).
func (o *Optimizer) Params(report core.PerformanceReport) core.AdaptiveParams {
	p := core.AdaptiveParams{
		MaxTotalPct:  o.cfg.NormalMaxTotalPct,
		BuyThreshold: o.cfg.NormalBuyThreshold,
	}

	var reasons []string

	// Rule 1 – DrawdownGuard.
	if report.MaxDrawdown > o.cfg.DrawdownThreshold {
		p.MaxTotalPct = o.cfg.ReducedMaxTotalPct
		reasons = append(reasons, fmt.Sprintf(
			"DRAWDOWN_GUARD(dd=%.1f%%>%.1f%% → maxPct %.0f%%→%.0f%%)",
			report.MaxDrawdown, o.cfg.DrawdownThreshold,
			o.cfg.NormalMaxTotalPct*100, o.cfg.ReducedMaxTotalPct*100))
	}

	// Rule 2 – WinRateGuard (only after MinTrades to avoid noise).
	if report.TradeCount >= o.cfg.MinTrades && report.WinRate < o.cfg.WinRateThreshold {
		p.BuyThreshold = o.cfg.RaisedBuyThreshold
		reasons = append(reasons, fmt.Sprintf(
			"WINRATE_GUARD(wr=%.1f%%<%.1f%% → threshold %.2f→%.2f)",
			report.WinRate, o.cfg.WinRateThreshold,
			o.cfg.NormalBuyThreshold, o.cfg.RaisedBuyThreshold))
	}

	if len(reasons) > 0 {
		p.LogReason = "⚙️  [Adaptive] " + reasons[0]
		for _, r := range reasons[1:] {
			p.LogReason += " | " + r
		}
	}
	return p
}
