// Package replay provides a DataProvider that replays historical market data
// from a CSV file or programmatically-added bars.
//
// CSV format (header required, in any column order):
//
//	date,symbol,open,high,low,close,volume
//
// Design:
//  1. LoadCSV populates a per-symbol bar list; GetRealtime advances through
//     the bars one tick at a time.
//  2. On the first call to GetRealtime for a symbol, the provider pre-warms
//     EMA20/Return5d/Return20d/Volatility using the first min(21, N) bars so
//     that derived fields are non-zero from the very first delivered quote.
//  3. When all bars are consumed, the last price is held (no panic).
//  4. Derived fields are computed identically to provider/mock so all
//     AlphaStrategy implementations see consistent inputs.
package replay

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"astock_trade/core"
)

// histLen is the price-history window (21 prices = 20 returns = enough for
// Return20d and Volatility(20)), matching provider/mock.
const histLen = 21

// emaAlpha is the EMA-20 smoothing factor: 2/(20+1).
const emaAlpha = 2.0 / 21.0

// Bar is one daily/intraday OHLCV record.
type Bar struct {
	Date   string
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
}

// symbolState holds rolling history for one symbol.
type symbolState struct {
	history       []float64
	volumeHistory []int64
	ema20         float64
	bars          []Bar
	idx           int  // index of the next bar to deliver
	warmed        bool // pre-warm performed?
}

func (s *symbolState) push(price float64, volume int64) {
	s.history = append(s.history, price)
	if len(s.history) > histLen {
		s.history = s.history[1:]
	}
	s.volumeHistory = append(s.volumeHistory, volume)
	if len(s.volumeHistory) > histLen {
		s.volumeHistory = s.volumeHistory[1:]
	}
	s.ema20 = emaAlpha*price + (1-emaAlpha)*s.ema20
}

func (s *symbolState) returnND(n int) float64 {
	l := len(s.history)
	if l <= n {
		return 0
	}
	past := s.history[l-1-n]
	if past <= 0 {
		return 0
	}
	return (s.history[l-1] - past) / past * 100
}

func (s *symbolState) volatility() float64 {
	n := len(s.history)
	if n < 2 {
		return 0
	}
	returns := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		if s.history[i] > 0 {
			returns[i] = (s.history[i+1] - s.history[i]) / s.history[i] * 100
		}
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))
	variance := 0.0
	for _, r := range returns {
		d := r - mean
		variance += d * d
	}
	variance /= float64(len(returns))
	v := math.Sqrt(variance)
	if math.IsNaN(v) {
		return 0
	}
	return v
}

func (s *symbolState) closeNDaysAgo(n int) float64 {
	l := len(s.history)
	if l <= n {
		return 0
	}
	return s.history[l-1-n]
}

func (s *symbolState) avgVolumeNDays(n int) float64 {
	l := len(s.volumeHistory)
	if n <= 0 || l <= n {
		return 0
	}
	start := l - 1 - n
	sum := float64(0)
	for i := start; i < l-1; i++ {
		sum += float64(s.volumeHistory[i])
	}
	return sum / float64(n)
}

// Provider satisfies core.DataProvider.
type Provider struct {
	mu     sync.Mutex
	states map[string]*symbolState
}

// New returns an empty Provider.  Call LoadCSV or AddBar before running.
func New() *Provider {
	return &Provider{
		states: make(map[string]*symbolState),
	}
}

// LoadCSV loads OHLCV bars from a CSV file.
// The file must have a header row; columns may appear in any order.
// Required columns: date, symbol, open, high, low, close, volume.
// Existing data for a symbol is replaced.
func (p *Provider) LoadCSV(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("replay: open %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(bufio.NewReader(f))
	records, err := r.ReadAll()
	if err != nil {
		return fmt.Errorf("replay: parse CSV %s: %w", path, err)
	}
	if len(records) < 2 {
		return fmt.Errorf("replay: CSV %s has no data rows", path)
	}

	colIdx := make(map[string]int)
	for i, h := range records[0] {
		colIdx[h] = i
	}
	for _, col := range []string{"date", "symbol", "open", "high", "low", "close", "volume"} {
		if _, ok := colIdx[col]; !ok {
			return fmt.Errorf("replay: CSV missing required column %q", col)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Clear existing state for symbols that appear in this file.
	seen := make(map[string]bool)
	for _, row := range records[1:] {
		if len(row) <= colIdx["volume"] {
			continue
		}
		sym := row[colIdx["symbol"]]
		if !seen[sym] {
			seen[sym] = true
			p.states[sym] = &symbolState{
				history:       make([]float64, 0, histLen),
				volumeHistory: make([]int64, 0, histLen),
			}
		}
	}

	for _, row := range records[1:] {
		if len(row) <= colIdx["volume"] {
			continue
		}
		sym := row[colIdx["symbol"]]
		open, _ := strconv.ParseFloat(row[colIdx["open"]], 64)
		high, _ := strconv.ParseFloat(row[colIdx["high"]], 64)
		low, _ := strconv.ParseFloat(row[colIdx["low"]], 64)
		closeP, _ := strconv.ParseFloat(row[colIdx["close"]], 64)
		vol, _ := strconv.ParseInt(row[colIdx["volume"]], 10, 64)

		p.states[sym].bars = append(p.states[sym].bars, Bar{
			Date:   row[colIdx["date"]],
			Open:   open,
			High:   high,
			Low:    low,
			Close:  closeP,
			Volume: vol,
		})
	}

	return nil
}

// AddBar appends a single bar for a symbol.
// Useful for synthetic data generation in tests or demos.
func (p *Provider) AddBar(symbol string, bar Bar) {
	p.mu.Lock()
	defer p.mu.Unlock()

	s, ok := p.states[symbol]
	if !ok {
		s = &symbolState{history: make([]float64, 0, histLen), volumeHistory: make([]int64, 0, histLen)}
		p.states[symbol] = s
	}
	s.bars = append(s.bars, bar)
}

// GetRealtime satisfies core.DataProvider.
// Each call advances to the next bar for every requested symbol.
// When data is exhausted the last available bar is held (price unchanged).
func (p *Provider) GetRealtime(symbols []string) map[string]*core.Quote {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make(map[string]*core.Quote, len(symbols))
	now := time.Now().UnixMilli()

	for _, sym := range symbols {
		s, ok := p.states[sym]
		if !ok || len(s.bars) == 0 {
			continue
		}

		// Pre-warm: push first min(histLen, N) bars without delivering quotes.
		if !s.warmed {
			s.warmed = true
			warmLen := histLen
			if len(s.bars) < warmLen {
				warmLen = len(s.bars)
			}
			s.ema20 = s.bars[0].Close
			for i := 0; i < warmLen; i++ {
				s.push(s.bars[i].Close, s.bars[i].Volume)
			}
			s.idx = warmLen
		}

		// Clamp idx to last bar when exhausted.
		if s.idx >= len(s.bars) {
			s.idx = len(s.bars) - 1
		}

		bar := s.bars[s.idx]
		prevClose := bar.Open
		if s.idx > 0 {
			prevClose = s.bars[s.idx-1].Close
		}
		s.push(bar.Close, bar.Volume)
		s.idx++

		spread := bar.Close * 0.001
		pctChg := 0.0
		if prevClose > 0 {
			pctChg = (bar.Close - prevClose) / prevClose * 100
		}

		avgVolume5d := s.avgVolumeNDays(5)
		volumeRatio := 0.0
		if avgVolume5d > 0 {
			volumeRatio = float64(bar.Volume) / avgVolume5d
		}

		result[sym] = &core.Quote{
			Symbol:      sym,
			Price:       bar.Close,
			PrevClose:   prevClose,
			Bid1:        bar.Close - spread,
			Ask1:        bar.Close + spread,
			Volume:      bar.Volume,
			PctChg:      pctChg,
			Return5d:    s.returnND(5),
			Return20d:   s.returnND(20),
			EMA20:       s.ema20,
			Volatility:  s.volatility(),
			AvgVolume5d: avgVolume5d,
			VolumeRatio: volumeRatio,
			Timestamp:   now,
		}
	}
	return result
}

// DiagnosticInputs returns same-tick raw inputs for factor diagnostics.
func (p *Provider) DiagnosticInputs(symbols []string) map[string]core.FactorDiagnosticInput {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make(map[string]core.FactorDiagnosticInput, len(symbols))
	for _, sym := range symbols {
		s, ok := p.states[sym]
		if !ok || len(s.history) == 0 || s.idx == 0 || s.idx > len(s.bars) {
			continue
		}
		bar := s.bars[s.idx-1]
		input := core.FactorDiagnosticInput{
			Symbol:        sym,
			Close:         bar.Close,
			Close1dAgo:    s.closeNDaysAgo(1),
			Close5dAgo:    s.closeNDaysAgo(5),
			Close20dAgo:   s.closeNDaysAgo(20),
			VolumeToday:   bar.Volume,
			AvgVolume5d:   s.avgVolumeNDays(5),
			EMA20:         s.ema20,
			VolatilityRaw: s.volatility(),
		}
		if input.Close1dAgo > 0 {
			input.PctChg = (input.Close/input.Close1dAgo - 1) * 100
		}
		if input.Close5dAgo > 0 {
			input.Return5dRaw = (input.Close/input.Close5dAgo - 1) * 100
		}
		if input.Close20dAgo > 0 {
			input.Return20dRaw = (input.Close/input.Close20dAgo - 1) * 100
		}
		if input.AvgVolume5d > 0 {
			input.VolumeRatio = float64(input.VolumeToday) / input.AvgVolume5d
		}
		out[sym] = input
	}
	return out
}

// Done returns true when all symbols have exhausted their bars.
func (p *Provider) Done() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.states {
		if s.idx < len(s.bars) {
			return false
		}
	}
	return len(p.states) > 0
}

// CurrentDate returns the date string of the last delivered bar for symbol.
// Returns "" if the symbol is unknown or no bar has been delivered yet.
func (p *Provider) CurrentDate(symbol string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.states[symbol]
	if !ok || s.idx == 0 || s.idx > len(s.bars) {
		return ""
	}
	return s.bars[s.idx-1].Date
}

// BarCount returns the total number of bars loaded for symbol.
func (p *Provider) BarCount(symbol string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.states[symbol]; ok {
		return len(s.bars)
	}
	return 0
}
