// Package datacheck implements a data-integrity firewall that runs before every
// Alpha scoring and order-generation cycle.
//
// Design principle: the trading engine must NEVER act on corrupt data.
// If any check fails, BUY orders are suppressed for the current tick while
// SELL / risk-exit orders continue unaffected (capital protection always wins).
//
// Five check categories (all must pass to allow new entries):
//
//  1. Index price  – 000300 (or configured IndexSymbol) price must be > 0.
//     A zero price means the provider has no data; MarketFilter would produce
//     unreliable regime classification.
//
//  2. Quote freshness – every quote's Timestamp must be within today (CST).
//     Stale yesterday-close prices must not trigger new entries.
//
//  3. Volume sanity – every stock must have Volume > 0.
//     Additionally, if ≥ 90 % of stocks share the same volume value the data
//     is treated as frozen/replicated and rejected.
//
//  4. Price sanity – prices must be positive.
//     (Full cross-source deviation checking is a future enhancement; the
//     single-source check is implemented here.)
//
//  5. Factor distribution – for each derived factor (PctChg, Return5d,
//     Return20d, EMA20, Volatility) the checker computes mean / std / min / max.
//     A near-zero std means every stock has the same value → factor is degenerate.
package datacheck

import (
	"fmt"
	"math"
	"sort"
	"time"

	"astock_trade/core"
)

// ── Configuration ─────────────────────────────────────────────────────────────

// Config controls the thresholds used by every check.
type Config struct {
	// IndexSymbol is the market-index code whose price must be > 0.
	// Typically "000300" (CSI 300).  Empty string disables the index check.
	IndexSymbol string

	// MaxQuoteAgeHours is the maximum permitted age of a quote's timestamp.
	// Quotes older than this are considered stale.  Default 36 h (covers
	// pre-market and overnight cache; tighter values suit pure intraday use).
	MaxQuoteAgeHours float64

	// MinFactorStd is the minimum acceptable standard deviation for a factor
	// distribution across all stocks.  If std < MinFactorStd the factor is
	// declared degenerate (all stocks have ~same value → useless signal).
	// Default 1e-6.
	MinFactorStd float64

	// VolumeUniformThreshold: if this fraction (0–1) of stocks share the
	// identical volume value, the batch is flagged as frozen/replicated.
	// Default 0.9.
	VolumeUniformThreshold float64

	// MinStockCount is the minimum number of quotes required to run factor
	// distribution checks.  Below this the checks are skipped (not enough
	// data for meaningful statistics).  Default 3.
	MinStockCount int
}

// Default returns a Config with production-safe defaults.
func Default() Config {
	return Config{
		IndexSymbol:            "000300",
		MaxQuoteAgeHours:       36,
		MinFactorStd:           1e-6,
		VolumeUniformThreshold: 0.9,
		MinStockCount:          3,
	}
}

// ── Result types ──────────────────────────────────────────────────────────────

// FactorStats summarises the distribution of one derived factor across all
// stocks in the current tick.
type FactorStats struct {
	Name  string
	Count int
	Mean  float64
	Std   float64
	Min   float64
	Max   float64
}

func (f FactorStats) String() string {
	return fmt.Sprintf("%s: n=%d  mean=%+.4f  std=%.4f  [%.4f, %.4f]",
		f.Name, f.Count, f.Mean, f.Std, f.Min, f.Max)
}

// CheckResult is the outcome of one full validation pass.
// OK is true only when every enabled check passes.
// Errors must be resolved before BUY orders are allowed.
// Warnings are advisory; they do not block trading.
type CheckResult struct {
	OK       bool
	Errors   []string
	Warnings []string
	Factors  []FactorStats // distribution stats for each derived factor
}

// ── Checker ───────────────────────────────────────────────────────────────────

// Checker is stateless; it is safe to call Check concurrently from multiple
// goroutines.
type Checker struct {
	cfg Config
}

// New returns a Checker with the supplied configuration.
func New(cfg Config) *Checker {
	if cfg.MaxQuoteAgeHours == 0 {
		cfg.MaxQuoteAgeHours = Default().MaxQuoteAgeHours
	}
	if cfg.MinFactorStd == 0 {
		cfg.MinFactorStd = Default().MinFactorStd
	}
	if cfg.VolumeUniformThreshold == 0 {
		cfg.VolumeUniformThreshold = Default().VolumeUniformThreshold
	}
	if cfg.MinStockCount == 0 {
		cfg.MinStockCount = Default().MinStockCount
	}
	return &Checker{cfg: cfg}
}

// Check runs all configured checks against the current tick's market data.
//
//   - stockQuotes: per-symbol quotes for all screened + position stocks.
//   - indexQuote:  quote for the market index (may be nil if not configured).
//
// The returned CheckResult.OK is false when any check fails.
// Trading logic should inspect OK before generating BUY orders.
func (c *Checker) Check(stockQuotes map[string]*core.Quote, indexQuote *core.Quote) CheckResult {
	result := CheckResult{OK: true}
	now := time.Now()

	// ── Check 1: Index price ──────────────────────────────────────────────────
	if c.cfg.IndexSymbol != "" {
		if indexQuote == nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("[DataCheck] ❌ 指数行情缺失 symbol=%s — provider 未返回数据，MarketFilter 不可用，禁止开仓",
					c.cfg.IndexSymbol))
		} else if indexQuote.Price <= 0 {
			result.Errors = append(result.Errors,
				fmt.Sprintf("[DataCheck] ❌ 指数价格无效 symbol=%s price=%.4f — 返回0或负值，MarketFilter 不可用，禁止开仓",
					c.cfg.IndexSymbol, indexQuote.Price))
		} else {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("[DataCheck] ✅ 指数价格正常 %s=%.2f", c.cfg.IndexSymbol, indexQuote.Price))
		}
	}

	if len(stockQuotes) == 0 {
		result.Errors = append(result.Errors,
			"[DataCheck] ❌ 全部股票行情为空 — provider 无数据返回，禁止开仓")
		result.OK = len(result.Errors) == 0
		return result
	}

	// ── Check 2: Quote freshness (timestamp must be within today) ─────────────
	staleCount := 0
	cutoff := now.Add(-time.Duration(float64(time.Hour) * c.cfg.MaxQuoteAgeHours))
	for sym, q := range stockQuotes {
		if q == nil {
			continue
		}
		ts := time.UnixMilli(q.Timestamp)
		if ts.Before(cutoff) {
			staleCount++
			if staleCount <= 3 { // cap error list to avoid spam
				result.Errors = append(result.Errors,
					fmt.Sprintf("[DataCheck] ❌ 行情过时 symbol=%s ts=%s (age=%.1fh > %.0fh)",
						sym, ts.Format("01-02 15:04:05"),
						now.Sub(ts).Hours(), c.cfg.MaxQuoteAgeHours))
			}
		}
		// Warn if quote is from yesterday's close (>15h old) but within cutoff
		if !ts.Before(cutoff) && now.Sub(ts) > 15*time.Hour {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("[DataCheck] ⚠️  行情陈旧 symbol=%s ts=%s (age=%.1fh，可能为昨收价)",
					sym, ts.Format("15:04:05"), now.Sub(ts).Hours()))
		}
	}
	if staleCount > 3 {
		result.Errors = append(result.Errors,
			fmt.Sprintf("[DataCheck] ❌ 共 %d 只股票行情过时（仅显示前3条）", staleCount))
	}

	// ── Check 3: Price > 0 ────────────────────────────────────────────────────
	zeroPriceSyms := make([]string, 0)
	for sym, q := range stockQuotes {
		if q == nil || q.Price <= 0 {
			zeroPriceSyms = append(zeroPriceSyms, sym)
		}
	}
	if len(zeroPriceSyms) > 0 {
		sort.Strings(zeroPriceSyms)
		shown := zeroPriceSyms
		suffix := ""
		if len(shown) > 5 {
			shown, suffix = shown[:5], fmt.Sprintf(" …共%d只", len(zeroPriceSyms))
		}
		result.Errors = append(result.Errors,
			fmt.Sprintf("[DataCheck] ❌ 价格为0或缺失 symbols=%v%s — 这些标的将跳过评分", shown, suffix))
	}

	// ── Check 4: Volume > 0 and not uniformly identical ───────────────────────
	zeroVolSyms := make([]string, 0)
	volFreq := make(map[int64]int)
	totalValid := 0
	for sym, q := range stockQuotes {
		if q == nil {
			continue
		}
		totalValid++
		if q.Volume <= 0 {
			zeroVolSyms = append(zeroVolSyms, sym)
		}
		volFreq[q.Volume]++
	}
	if len(zeroVolSyms) > 0 {
		sort.Strings(zeroVolSyms)
		shown := zeroVolSyms
		suffix := ""
		if len(shown) > 5 {
			shown, suffix = shown[:5], fmt.Sprintf(" …共%d只", len(zeroVolSyms))
		}
		result.Errors = append(result.Errors,
			fmt.Sprintf("[DataCheck] ❌ 成交量为0 symbols=%v%s — 非交易时段或数据异常", shown, suffix))
	}
	// Uniformity: if one volume value dominates > threshold → frozen data
	if totalValid > 0 {
		for vol, cnt := range volFreq {
			if float64(cnt)/float64(totalValid) >= c.cfg.VolumeUniformThreshold {
				result.Errors = append(result.Errors,
					fmt.Sprintf("[DataCheck] ❌ 成交量数据异常 %.0f%%股票成交量相同(=%d) — 疑似数据冻结或复制",
						float64(cnt)/float64(totalValid)*100, vol))
				break
			}
		}
	}

	// ── Check 5: Factor distribution ─────────────────────────────────────────
	if totalValid >= c.cfg.MinStockCount {
		factors := []struct {
			name string
			fn   func(*core.Quote) float64
		}{
			{"PctChg", func(q *core.Quote) float64 { return q.PctChg }},
			{"Return5d", func(q *core.Quote) float64 { return q.Return5d }},
			{"Return20d", func(q *core.Quote) float64 { return q.Return20d }},
			{"EMA20", func(q *core.Quote) float64 { return q.EMA20 }},
			{"Volatility", func(q *core.Quote) float64 { return q.Volatility }},
		}

		for _, f := range factors {
			vals := make([]float64, 0, totalValid)
			for _, q := range stockQuotes {
				if q != nil && q.Price > 0 {
					vals = append(vals, f.fn(q))
				}
			}
			if len(vals) < 2 {
				continue
			}
			mean, std, mn, mx := distStats(vals)
			fs := FactorStats{
				Name:  f.name,
				Count: len(vals),
				Mean:  mean,
				Std:   std,
				Min:   mn,
				Max:   mx,
			}
			result.Factors = append(result.Factors, fs)

			if std < c.cfg.MinFactorStd {
				result.Errors = append(result.Errors,
					fmt.Sprintf("[DataCheck] ❌ 因子退化 factor=%s std=%.2e≈0 (所有股票值相同=%.4f) — 因子失效，禁止开仓",
						f.name, std, mean))
			} else if allSameSign(vals) && f.name != "EMA20" {
				// All-positive or all-negative is suspicious for return factors
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("[DataCheck] ⚠️  因子单边分布 factor=%s mean=%+.2f std=%.2f — 数据可能存在偏差",
						f.name, mean, std))
			}
		}
	}

	result.OK = len(result.Errors) == 0
	return result
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// distStats computes mean, std, min, max for a slice of float64.
// Panics if v is empty; callers must ensure len(v) >= 1.
func distStats(v []float64) (mean, std, min, max float64) {
	min, max = v[0], v[0]
	sum := 0.0
	for _, x := range v {
		sum += x
		if x < min {
			min = x
		}
		if x > max {
			max = x
		}
	}
	mean = sum / float64(len(v))
	varSum := 0.0
	for _, x := range v {
		d := x - mean
		varSum += d * d
	}
	std = math.Sqrt(varSum / float64(len(v)))
	return
}

// allSameSign returns true when all values are strictly positive or all
// strictly negative (ignoring near-zero values within ±0.01).
func allSameSign(v []float64) bool {
	if len(v) == 0 {
		return false
	}
	pos, neg := 0, 0
	for _, x := range v {
		if x > 0.01 {
			pos++
		} else if x < -0.01 {
			neg++
		}
	}
	if pos == 0 && neg == 0 {
		return false // all near zero
	}
	return pos == 0 || neg == 0
}
