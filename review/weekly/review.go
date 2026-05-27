// Package weekly provides a Reviewer that reads the trade log and prints a
// structured weekly summary. Extend this to compute Sharpe ratio, max drawdown,
// win-rate, and export to CSV/HTML without changing any other package.
package weekly

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"astock_trade/core"
)

// Reviewer satisfies core.Reviewer.
type Reviewer struct {
	logPath string
}

// New returns a Reviewer that reads trades from logPath.
func New(logPath string) *Reviewer {
	return &Reviewer{logPath: logPath}
}

// summary is the aggregate computed over a set of trades.
type summary struct {
	ISOWeek    int
	TotalTrades int
	BuyCount   int
	SellCount  int
	GrossVol   float64         // sum of (price × quantity) for all fills
	Symbols    map[string]int  // symbol → trade count
}

// Review scans the entire trade log and prints a report to stdout.
// For production use, restrict scanning to the current ISO week only, or
// maintain a rolling stats store.
func (r *Reviewer) Review() error {
	f, err := os.Open(r.logPath)
	if err != nil {
		return fmt.Errorf("open trade log: %w", err)
	}
	defer f.Close()

	_, week := time.Now().ISOWeek()
	s := summary{
		ISOWeek: week,
		Symbols: make(map[string]int),
	}

	dec := json.NewDecoder(f)
	for {
		var t core.Trade
		if err := dec.Decode(&t); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("decode trade: %w", err)
		}
		s.TotalTrades++
		s.GrossVol += t.Price * float64(t.Quantity)
		s.Symbols[t.Symbol]++
		if t.Side == "BUY" {
			s.BuyCount++
		} else {
			s.SellCount++
		}
	}

	log.Printf(
		"[WeeklyReview] ISOWeek=%d  Trades=%d (B=%d S=%d)  GrossVol=%.2f  Symbols=%d",
		s.ISOWeek, s.TotalTrades, s.BuyCount, s.SellCount, s.GrossVol, len(s.Symbols),
	)
	for sym, cnt := range s.Symbols {
		log.Printf("[WeeklyReview]   %-8s %d trades", sym, cnt)
	}
	return nil
}
