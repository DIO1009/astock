// Package position provides the in-memory position book and exit-signal logic.
// Thread-safe via sync.RWMutex.
//
// T+1 enforcement (A-share rule):
//   - On BUY:  SellableQty is NOT increased. New shares are locked for the rest
//              of the current trading day (BuyTradeDay = currentTradeDay).
//   - On AdvanceTradeDay(day): any position with BuyTradeDay < day is unlocked
//              (SellableQty = Quantity). Called by the engine at tick start.
//   - On SELL: only SellableQty shares can be sold; the sell quantity is
//              clamped to SellableQty by the engine before calling ApplyTrade.
//
// Design invariant: the only public mutation path is ApplyTrade. This enforces
// the architectural rule that all position changes must originate from an
// executed Trade, never directly from a Strategy signal.
package position

import (
	"log"
	"sync"

	"astock_trade/core"
)

// Config holds risk-management thresholds expressed as decimal fractions.
//
// Exit logic priority (evaluated in order each tick):
//  1. STOP_LOSS  – pnlPct ≤ −StopLossPct         (hard floor; cuts losses fast)
//  2. TAKE_PROFIT – pnlPct ≥ TakeProfitPct        (fixed target; books profit)
//  3. TRAIL_STOP  – only AFTER pnlPct ≥ TrailStart:
//                   if drawdown from HighestPrice ≥ TrailDrop → exit
//                   (lets winners run, exits on reversal)
type Config struct {
	// StopLossPct: hard exit on loss. Example: 0.06 = −6%.
	StopLossPct float64
	// TakeProfitPct: fixed-target exit on gain. Example: 0.08 = +8%.
	TakeProfitPct float64
	// TrailStart: profit level at which the trailing stop becomes active.
	// Example: 0.05 → trailing stop only kicks in once position is up ≥5%.
	TrailStart float64
	// TrailDrop: once trailing is active, exit if price falls this far from
	// HighestPrice. Example: 0.03 → exit on 3% drawdown from peak.
	TrailDrop float64
}

// Manager satisfies core.PositionManager.
type Manager struct {
	mu             sync.RWMutex
	positions      map[string]*core.Position
	cfg            Config
	currentTradeDay int64 // set by AdvanceTradeDay; 0 = not yet initialised
}

// New returns an empty Manager with the provided configuration.
func New(cfg Config) *Manager {
	return &Manager{
		positions: make(map[string]*core.Position),
		cfg:       cfg,
	}
}

// AdvanceTradeDay unlocks T+1 positions for the new trading day.
// Must be called by the engine at the start of each tick before any trades.
//
// Logic: for every position where BuyTradeDay < currentDay, set
// SellableQty = Quantity (the full holding is now available to sell).
func (m *Manager) AdvanceTradeDay(currentDay int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if currentDay <= m.currentTradeDay {
		return // same day or going backwards – no-op
	}
	m.currentTradeDay = currentDay

	for sym, pos := range m.positions {
		if pos.BuyTradeDay < currentDay && pos.SellableQty < pos.Quantity {
			pos.SellableQty = pos.Quantity
			log.Printf("[T+1] %-8s 解锁  SellableQty=%d  (买入日=%d → 今日=%d)",
				sym, pos.SellableQty, pos.BuyTradeDay, currentDay)
		}
	}
}

// ApplyTrade is the single entry-point for all position mutations.
//
// BUY  → if no existing position, open one; if position exists, merge and
//
//	recalculate the weighted average cost (AvgPrice).
//	SellableQty is NOT increased – new shares are locked until the next
//	trading day (T+1 rule).
//
// SELL → reduce quantity; SellableQty is decreased equally.
//
//	Close the position when quantity reaches zero.
//	The caller (engine) is responsible for clamping trade.Quantity to
//	pos.SellableQty before calling ApplyTrade.
func (m *Manager) ApplyTrade(trade *core.Trade) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch trade.Side {
	case "BUY":
		if pos, ok := m.positions[trade.Symbol]; ok {
			// Merge into existing position: recalculate weighted average cost.
			//   newAvg = (oldAvg×oldQty + fillPrice×fillQty) / (oldQty+fillQty)
			oldCost := pos.AvgPrice * float64(pos.Quantity)
			newCost := trade.Price * float64(trade.Quantity)
			pos.Quantity += trade.Quantity
			pos.AvgPrice = (oldCost + newCost) / float64(pos.Quantity)
			if trade.Price > pos.HighestPrice {
				pos.HighestPrice = trade.Price
			}
			// T+1: newly added shares are locked; SellableQty unchanged.
			pos.BuyTradeDay = m.currentTradeDay
		} else {
			// First fill for this symbol.
			m.positions[trade.Symbol] = &core.Position{
				Symbol:       trade.Symbol,
				EntryPrice:   trade.Price,
				AvgPrice:     trade.Price,
				HighestPrice: trade.Price,
				Quantity:     trade.Quantity,
				SellableQty:  0,                // T+1: locked until next trading day
				BuyTradeDay:  m.currentTradeDay,
			}
		}

	case "SELL":
		pos, ok := m.positions[trade.Symbol]
		if !ok {
			log.Printf("[PositionManager] SELL for unknown position %s – ignored", trade.Symbol)
			return
		}
		pos.Quantity -= trade.Quantity
		pos.SellableQty -= trade.Quantity
		if pos.SellableQty < 0 {
			pos.SellableQty = 0 // guard against over-sell (should not happen if engine clamps)
		}
		if pos.Quantity <= 0 {
			delete(m.positions, trade.Symbol)
		}
		// AvgPrice is unchanged on a sell (cost basis remains until position is closed).

	default:
		log.Printf("[PositionManager] unknown trade side %q for %s – ignored", trade.Side, trade.Symbol)
	}
}

// CheckExit evaluates exit conditions against the position's AvgPrice
// (weighted average cost), not the first-fill EntryPrice.
//
// Returns one of: "HOLD" | "STOP_LOSS" | "TAKE_PROFIT" | "TRAIL_STOP".
//
// TRAIL_STOP is intentionally gated behind TrailStart so the trailing stop
// never fires while the position is at a loss – it only "trails" a winner.
func (m *Manager) CheckExit(pos *core.Position, q *core.Quote) string {
	pnlPct := (q.Price - pos.AvgPrice) / pos.AvgPrice

	switch {
	case pnlPct <= -m.cfg.StopLossPct:
		return "STOP_LOSS"
	case pnlPct >= m.cfg.TakeProfitPct:
		return "TAKE_PROFIT"
	case pnlPct >= m.cfg.TrailStart:
		// Trailing stop only activates once position profit ≥ TrailStart.
		// This ensures we never trail a losing position.
		drawdown := (pos.HighestPrice - q.Price) / pos.HighestPrice
		if drawdown >= m.cfg.TrailDrop {
			return "TRAIL_STOP"
		}
	}
	return "HOLD"
}

// AllPositions returns a value-type snapshot of all open positions.
func (m *Manager) AllPositions() []core.Position {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]core.Position, 0, len(m.positions))
	for _, p := range m.positions {
		out = append(out, *p)
	}
	return out
}

// GetPosition returns the live position pointer and an existence flag.
func (m *Manager) GetPosition(symbol string) (*core.Position, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.positions[symbol]
	return p, ok
}

// HasPosition reports whether a position for the given symbol is currently open.
func (m *Manager) HasPosition(symbol string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.positions[symbol]
	return ok
}

// UpdateHighest raises the HighestPrice watermark; used by the engine after
// each tick to keep the trailing-stop reference current.
func (m *Manager) UpdateHighest(symbol string, price float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.positions[symbol]; ok && price > p.HighestPrice {
		p.HighestPrice = price
	}
}
