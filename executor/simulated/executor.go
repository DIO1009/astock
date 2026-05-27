// Package simulated provides an Executor that fills orders locally without
// connecting to a real broker.
//
// The Quote parameter enables realistic simulation:
//   - A-share circuit-breaker check (±10 % of PrevClose)
//   - Slippage relative to the live bid/ask spread
//   - Volume feasibility guard
package simulated

import (
	"fmt"
	"time"

	"astock_trade/core"
)

// Config holds execution simulation parameters.
type Config struct {
	// SlippagePct is the one-way slippage fraction applied to the fill price.
	// Example: 0.001 = 0.1 %.
	SlippagePct float64
	// CommissionPct is recorded in the Trade for P&L accounting but does not
	// alter the fill price in this skeleton.
	// Example: 0.0003 = 0.03 %.
	CommissionPct float64
}

// Executor satisfies core.Executor.
type Executor struct {
	cfg Config
}

// New returns an Executor with the provided configuration.
func New(cfg Config) *Executor {
	return &Executor{cfg: cfg}
}

// Execute simulates a market fill using the live Quote for context.
//
// Checks performed (skeleton – extend as needed):
//  1. A-share daily price limit: reject if order price is outside ±10 % of PrevClose.
//  2. Volume feasibility: reject if requested quantity exceeds 10 % of quoted volume.
//  3. Apply directional slippage: BUY fills higher, SELL fills lower.
//
// TODO: model partial fills, T+1 settlement, suspension handling.
func (e *Executor) Execute(order *core.Order, quote *core.Quote) (*core.Trade, error) {
	if order.Quantity <= 0 {
		return nil, fmt.Errorf("invalid quantity %d for %s", order.Quantity, order.Symbol)
	}
	if order.Price <= 0 {
		return nil, fmt.Errorf("invalid price %.4f for %s", order.Price, order.Symbol)
	}

	// Circuit-breaker guard (A-share ±10 % daily limit).
	if quote.PrevClose > 0 {
		upperLimit := quote.PrevClose * 1.10
		lowerLimit := quote.PrevClose * 0.90
		if order.Price > upperLimit || order.Price < lowerLimit {
			return nil, fmt.Errorf(
				"%s order price %.4f outside daily limit [%.4f, %.4f]",
				order.Symbol, order.Price, lowerLimit, upperLimit,
			)
		}
	}

	// Volume feasibility guard.
	if quote.Volume > 0 && int64(order.Quantity) > quote.Volume/10 {
		return nil, fmt.Errorf(
			"%s order qty %d exceeds 10%% of quoted volume %d",
			order.Symbol, order.Quantity, quote.Volume,
		)
	}

	fillPrice := order.Price
	switch order.Side {
	case "BUY":
		fillPrice *= 1 + e.cfg.SlippagePct
	case "SELL":
		fillPrice *= 1 - e.cfg.SlippagePct
	default:
		return nil, fmt.Errorf("unknown order side %q", order.Side)
	}

	return &core.Trade{
		Symbol:    order.Symbol,
		Side:      order.Side,
		Price:     fillPrice,
		Quantity:  order.Quantity,
		Reason:    order.Reason, // propagate reason for audit logging
		Timestamp: time.Now().UnixMilli(),
	}, nil
}
