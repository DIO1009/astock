// Package execctrl implements execution discipline: cooldown windows after sells,
// high-price re-entry blocking, minimum hold-time enforcement, and per-tick
// trade frequency caps.
//
// Design principles:
//   - Zero global state; all state is encapsulated in Controller.
//   - STOP_LOSS is a hard exit that bypasses every guard (it must always execute).
//   - Thread-safe via a single mutex.
package execctrl

import (
	"fmt"
	"log"
	"sync"
)

// Config holds all tunable execution-discipline parameters.
type Config struct {
	// CooldownTicksLoss is the number of ticks to wait after a STOP_LOSS
	// before allowing a re-buy of the same symbol.
	// Prevents "catching a falling knife" after a stop-loss exit.
	CooldownTicksLoss int

	// CooldownTicksProfit is the number of ticks to wait after a profitable
	// exit (TAKE_PROFIT or TRAIL_STOP) before allowing a re-buy.
	// Prevents high-price re-entry chasing after locking in a winner.
	CooldownTicksProfit int

	// HighPriceBlockTicks limits how long HIGH_PRICE_REENTRY is enforced after
	// a STOP_LOSS exit.  Once this many ticks have passed the guard is lifted
	// and re-entry at any price is allowed (momentum regime may have changed).
	// 0 means enforce indefinitely (original behaviour).
	HighPriceBlockTicks int

	// MinHoldTicks is the minimum number of ticks a position must be held
	// before a non-stop-loss exit is permitted.
	// Prevents panic-selling of freshly-opened positions on normal noise.
	// STOP_LOSS always bypasses this guard.
	MinHoldTicks int

	// MaxBuyPerTick caps the number of BUY orders executed in a single tick.
	MaxBuyPerTick int

	// MaxSellPerTick caps the number of non-stop-loss SELL orders per tick.
	// STOP_LOSS exits are NOT counted against this cap.
	MaxSellPerTick int
}

// sellRecord tracks the outcome of the last SELL for a symbol.
type sellRecord struct {
	tick     int    // tick number when the sell occurred
	price    float64 // execution price of the sell
	exitType string  // "STOP_LOSS" | "TAKE_PROFIT" | "TRAIL_STOP"
}

// Controller satisfies core.ExecController.
type Controller struct {
	mu  sync.Mutex
	cfg Config

	tick int // monotonically increasing; advanced once per engine tick

	// sellHistory[symbol] is the most recent sell record for that symbol.
	sellHistory map[string]*sellRecord
	// buyTick[symbol] is the tick number when the current open position was entered.
	buyTick map[string]int

	buysThisTick  int // reset each tick by AdvanceTick
	sellsThisTick int // reset each tick; STOP_LOSS exits not counted
}

// New creates a Controller with the provided configuration.
func New(cfg Config) *Controller {
	return &Controller{
		cfg:         cfg,
		sellHistory: make(map[string]*sellRecord),
		buyTick:     make(map[string]int),
	}
}

// AdvanceTick increments the internal tick counter and resets per-tick caps.
// Must be called exactly once at the start of each engine tick.
func (c *Controller) AdvanceTick() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tick++
	c.buysThisTick = 0
	c.sellsThisTick = 0
}

// AllowBuy returns (true, "") if buying symbol at price is currently allowed.
// Returns (false, reason) if blocked.
//
// Checks (in order):
//  1. Per-tick BUY cap (MAX_BUY_LIMIT)
//  2. Post-sell cooldown window (COOLDOWN)
//  3. High-price re-entry guard (HIGH_PRICE_REENTRY)
func (c *Controller) AllowBuy(symbol string, price float64) (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 1. Per-tick BUY frequency cap.
	if c.buysThisTick >= c.cfg.MaxBuyPerTick {
		return false, fmt.Sprintf("MAX_BUY_LIMIT(%d/%d)", c.buysThisTick, c.cfg.MaxBuyPerTick)
	}

	rec, hasSellHistory := c.sellHistory[symbol]
	if !hasSellHistory {
		return true, ""
	}

	// 2. Cooldown window: must wait N ticks after the last sell.
	requiredCooldown := c.cfg.CooldownTicksLoss
	if rec.exitType != "STOP_LOSS" {
		requiredCooldown = c.cfg.CooldownTicksProfit
	}
	ticksSinceSell := c.tick - rec.tick
	if ticksSinceSell < requiredCooldown {
		return false, fmt.Sprintf("COOLDOWN(%d/%d ticks, last_exit=%s)",
			ticksSinceSell, requiredCooldown, rec.exitType)
	}

	// 3. High-price re-entry guard (STOP_LOSS exits only).
	//
	// Rationale: after being stopped out (STOP_LOSS), we were wrong about direction.
	// Buying back above the stop price would be chasing a knife.
	//
	// After TRAIL_STOP / TAKE_PROFIT we locked in a profit — the stock may be in a
	// momentum uptrend; blocking re-entry above the exit price would miss continuation
	// moves. Only the cooldown window applies for profitable exits.
	//
	// Time-limit: if HighPriceBlockTicks > 0 and more than that many ticks have
	// elapsed since the STOP_LOSS, the guard is lifted — the regime may have
	// completely changed and permanent blocking would miss bull-run re-entries.
	if rec.exitType == "STOP_LOSS" && price > rec.price {
		if c.cfg.HighPriceBlockTicks > 0 && ticksSinceSell >= c.cfg.HighPriceBlockTicks {
			// Guard expired; allow re-entry in changed regime.
		} else {
			return false, fmt.Sprintf("HIGH_PRICE_REENTRY(now=%.4f > sell=%.4f, age=%d/%d ticks)",
				price, rec.price, ticksSinceSell, c.cfg.HighPriceBlockTicks)
		}
	}

	return true, ""
}

// AllowSell returns true if a SELL is permitted for the given exitType.
//
//   - "STOP_LOSS" always returns true (hard exit, all guards bypassed).
//   - Other exits are subject to MinHoldTicks and the per-tick sell cap.
func (c *Controller) AllowSell(symbol string, exitType string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// STOP_LOSS is a hard exit — it must execute regardless of hold time or caps.
	if exitType == "STOP_LOSS" {
		return true
	}

	// Per-tick sell cap for non-stop-loss exits.
	if c.sellsThisTick >= c.cfg.MaxSellPerTick {
		log.Printf("  ⏸ [ExecCtrl] SELL blocked  %-8s  MAX_SELL_LIMIT(%d/%d)  exit=%s",
			symbol, c.sellsThisTick, c.cfg.MaxSellPerTick, exitType)
		return false
	}

	// Minimum hold-time guard: prevents premature exits due to normal price noise.
	if bt, ok := c.buyTick[symbol]; ok {
		held := c.tick - bt
		if held < c.cfg.MinHoldTicks {
			log.Printf("  ⏳ [ExecCtrl] SELL blocked  %-8s  MIN_HOLD(%d/%d ticks)  exit=%s",
				symbol, held, c.cfg.MinHoldTicks, exitType)
			return false
		}
	}

	return true
}

// RecordBuy must be called immediately after a BUY trade is confirmed.
// Clears any previous sell history for the symbol (fresh position start).
func (c *Controller) RecordBuy(symbol string, price float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buyTick[symbol] = c.tick
	c.buysThisTick++
	// Fresh start: remove sell history so the new position is tracked cleanly.
	delete(c.sellHistory, symbol)
}

// RecordSell must be called immediately after a SELL trade is confirmed.
// Stores the exit details for subsequent cooldown and price-guard checks.
func (c *Controller) RecordSell(symbol string, price float64, exitType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sellHistory[symbol] = &sellRecord{
		tick:     c.tick,
		price:    price,
		exitType: exitType,
	}
	delete(c.buyTick, symbol)
	if exitType != "STOP_LOSS" {
		c.sellsThisTick++
	}
}

// GetHoldTicks returns the number of ticks the current open position has been
// held. Returns 0 if there is no buy record for the symbol.
// Must be called BEFORE RecordSell to get a valid result.
func (c *Controller) GetHoldTicks(symbol string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	bt, ok := c.buyTick[symbol]
	if !ok {
		return 0
	}
	return c.tick - bt
}

// CurrentTick returns the current tick number (useful for external logging).
func (c *Controller) CurrentTick() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tick
}
