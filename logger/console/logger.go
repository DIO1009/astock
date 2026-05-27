// Package console provides a TradeLogger that writes every trade to stdout.
// It now prints the Reason field so every fill has a clear audit trail.
package console

import (
	"log"
	"time"

	"astock_trade/core"
)

// Logger satisfies core.TradeLogger.
type Logger struct{}

// New returns a Logger.
func New() *Logger { return &Logger{} }

// Log prints one trade line per fill, including the originating reason.
//
// Example output:
//
//	[Trade] 14:32:07.123  BUY   600519  qty=1116  price=  89.5820  ALPHA rank#1 score=+0.3160 stable=4
//	[Trade] 14:32:09.456  SELL  300750  qty=4073  price=  23.8800  STOP_LOSS avg=25.00 now=23.75 pnl=-5.00%
func (l *Logger) Log(trade *core.Trade) {
	ts := time.UnixMilli(trade.Timestamp).Format("15:04:05.000")
	log.Printf("  ★ [Trade] %s  %-4s  %-8s  qty=%5d  price=%9.4f  ← %s",
		ts, trade.Side, trade.Symbol, trade.Quantity, trade.Price, trade.Reason)
}
