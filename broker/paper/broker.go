// Package paper provides a Paper Trading Broker that wraps a core.Executor
// and records detailed execution data for slippage analysis and audit.
//
// The Broker implements core.Executor so it can be used as a drop-in
// replacement for simulated/realistic executors in the engine without any
// wiring changes.
//
// It additionally implements core.Broker so callers can use the richer
// PlaceOrder / CancelOrder / QueryPosition API when desired.
//
// Execution records are stored in memory and accessible via Records().
// An optional logger callback (set via SetLogger) is called for each record,
// allowing the caller to persist records to disk.
package paper

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"astock_trade/core"
)

// pendingOrder tracks an order placed via PlaceOrder that has not yet been
// filled or cancelled.
type pendingOrder struct {
	order core.Order
	id    string
}

// Broker wraps core.Executor with Paper Trading instrumentation.
//
// Concurrency: all public methods are safe for concurrent use.
type Broker struct {
	inner   core.Executor
	mu      sync.RWMutex
	records []core.ExecutionRecord
	pending map[string]*pendingOrder // orderID → pending order
	counter int64                    // atomic; generates unique order IDs
	session string                   // startup-scoped prefix; prevents ID reuse across restarts

	// logger is an optional per-record callback (e.g. write jsonl to disk).
	logger func(*core.ExecutionRecord)

	// positions mirrors filled trades for QueryPosition.
	// This is a simplified reconciliation store; in live mode, the canonical
	// source is PositionManager.
	positions map[string]*core.Position

	// account tracks the simulated cash balance for QueryAccount.
	cash   float64
	equity float64
}

// New returns a Paper Broker backed by the given Executor.
// initialCash should match PortfolioManager.TotalCapital.
func New(inner core.Executor, initialCash float64) *Broker {
	return &Broker{
		inner:     inner,
		pending:   make(map[string]*pendingOrder),
		positions: make(map[string]*core.Position),
		session:   time.Now().UTC().Format("20060102T150405.000000000"),
		cash:      initialCash,
		equity:    initialCash,
	}
}

// SetLogger registers an optional callback that is called synchronously for
// every ExecutionRecord (filled, partial, or rejected).
// Use this to persist records to jsonl via logger/execution.
func (b *Broker) SetLogger(f func(*core.ExecutionRecord)) {
	b.mu.Lock()
	b.logger = f
	b.mu.Unlock()
}

// ─── core.Executor implementation ────────────────────────────────────────────

// Execute implements core.Executor.
// It delegates to the inner Executor, captures timing and slippage, and records
// the result.  This is the primary method used by the engine.
func (b *Broker) Execute(order *core.Order, quote *core.Quote) (*core.Trade, error) {
	signalTime := quote.Timestamp
	if signalTime == 0 {
		signalTime = time.Now().UnixMilli()
	}
	orderTime := time.Now().UnixMilli()

	trade, err := b.inner.Execute(order, quote)
	execTime := time.Now().UnixMilli()

	orderID := b.nextOrderID()

	status := "FILLED"
	actualPrice := order.Price
	filledQty := 0

	if err != nil {
		status = "REJECTED"
	} else if trade != nil {
		actualPrice = trade.Price
		filledQty = trade.Quantity
		if trade.Quantity < order.Quantity {
			status = "PARTIAL"
		}
	}

	slippagePct := 0.0
	if order.Price > 0 {
		// Positive → paid more (unfavorable for BUY, favorable for SELL).
		slippagePct = (actualPrice - order.Price) / order.Price * 100
	}

	fillRate := 0.0
	if order.Quantity > 0 {
		fillRate = float64(filledQty) / float64(order.Quantity) * 100
	}

	// Use trade.Timestamp for latency so simulated execution delay
	// (set by realistic.Executor as now+delayMs) is visible in the record.
	latency := execTime - signalTime
	if trade != nil && trade.Timestamp > execTime {
		latency = trade.Timestamp - signalTime
	}

	rec := &core.ExecutionRecord{
		OrderID:          orderID,
		Symbol:           order.Symbol,
		Side:             order.Side,
		TheoreticalPrice: order.Price,
		ActualPrice:      actualPrice,
		SlippagePct:      slippagePct,
		OrderQty:         order.Quantity,
		FilledQty:        filledQty,
		FillRate:         fillRate,
		Status:           status,
		SignalTime:       signalTime,
		OrderTime:        orderTime,
		ExecutionTime:    execTime,
		Latency:          latency,
		Reason:           order.Reason,
	}

	b.mu.Lock()
	b.records = append(b.records, *rec)
	if b.logger != nil {
		b.logger(rec)
	}
	// Update simplified position mirror.
	if trade != nil {
		b.applyTrade(trade)
	}
	b.mu.Unlock()

	return trade, err
}

// applyTrade updates the internal position and cash mirrors.
// Must be called with b.mu held.
func (b *Broker) applyTrade(t *core.Trade) {
	cost := t.Price * float64(t.Quantity)
	switch t.Side {
	case "BUY":
		b.cash -= cost
		pos, ok := b.positions[t.Symbol]
		if !ok {
			pos = &core.Position{Symbol: t.Symbol, EntryPrice: t.Price}
			b.positions[t.Symbol] = pos
		}
		totalQty := pos.Quantity + t.Quantity
		if totalQty > 0 {
			pos.AvgPrice = (pos.AvgPrice*float64(pos.Quantity) + t.Price*float64(t.Quantity)) /
				float64(totalQty)
		}
		pos.Quantity = totalQty
		if t.Price > pos.HighestPrice {
			pos.HighestPrice = t.Price
		}
	case "SELL":
		b.cash += cost
		if pos, ok := b.positions[t.Symbol]; ok {
			pos.Quantity -= t.Quantity
			if pos.Quantity <= 0 {
				delete(b.positions, t.Symbol)
			}
		}
	}
}

// ─── core.Broker implementation ───────────────────────────────────────────────

// PlaceOrder implements core.Broker.
// In Paper Trading mode it immediately attempts execution by calling Execute
// with a synthetic quote based on the order price.
//
// For full async simulation, callers should use Execute directly.
func (b *Broker) PlaceOrder(order *core.Order) (string, error) {
	orderID := b.nextOrderID()
	b.mu.Lock()
	b.pending[orderID] = &pendingOrder{order: *order, id: orderID}
	b.mu.Unlock()
	return orderID, nil
}

func (b *Broker) nextOrderID() string {
	seq := atomic.AddInt64(&b.counter, 1)
	return fmt.Sprintf("ORD-%s-%06d", b.session, seq)
}

// CancelOrder implements core.Broker.
// Removes a pending order; returns error if already filled or not found.
func (b *Broker) CancelOrder(orderID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.pending[orderID]; !ok {
		return fmt.Errorf("paper broker: order %s not found (already filled or unknown)", orderID)
	}
	delete(b.pending, orderID)
	return nil
}

// QueryPosition implements core.Broker.
// Returns the broker-side position mirror (may lag PositionManager by one tick).
func (b *Broker) QueryPosition(symbol string) (*core.Position, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	pos, ok := b.positions[symbol]
	if !ok {
		return nil, false
	}
	copy := *pos
	return &copy, true
}

// QueryAccount implements core.Broker.
func (b *Broker) QueryAccount() (cash float64, equity float64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cash, b.equity
}

// ─── Inspection helpers ────────────────────────────────────────────────────────

// Records returns a defensive copy of all execution records collected so far.
func (b *Broker) Records() []core.ExecutionRecord {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]core.ExecutionRecord, len(b.records))
	copy(out, b.records)
	return out
}

// RestoreRecords replaces the in-memory execution history from persisted logs.
// It should be called only during startup, before new executions are recorded.
func (b *Broker) RestoreRecords(records []core.ExecutionRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records = append(b.records[:0], records...)
}

// Stats returns a quick summary of execution outcomes.
func (b *Broker) Stats() (total, filled, partial, rejected int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, r := range b.records {
		total++
		switch r.Status {
		case "FILLED":
			filled++
		case "PARTIAL":
			partial++
		case "REJECTED":
			rejected++
		}
	}
	return
}
