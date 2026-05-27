package universe

import (
	"math"
	"strings"
)

// ScoredStock is a StockInfo with a computed composite alpha score.
type ScoredStock struct {
	StockInfo
	Score float64
	Rank  int
}

// ── Scoring constants ─────────────────────────────────────────────────────────

// Factor weights (must sum to 1.0).
const (
	wRet5d       = 0.30
	wRet20d      = 0.25
	wVolumeRatio = 0.20
	wTurnover    = 0.10
	wChangeP     = 0.15
)

// Layer 1 – hard filter thresholds (applied before scoring).
const (
	minPrice    = 3.0          // CNY – exclude penny stocks
	minMktCap   = 3_000_000_000 // 30 亿 CNY
	minTurnover = 0.1          // turnover rate %
)

// Layer 2 – tighter thresholds (applied after ranking).
const (
	l2MinPrice    = 5.0
	l2MinMktCap   = 5_000_000_000  // 50 亿 CNY
	l2MaxMktCap   = 500_000_000_000 // 5 000 亿 CNY (avoid ultra-large caps)
	l2MinTurnover = 0.3
	l2MaxTurnover = 25.0 // >25% is likely speculative/trap
)

// FilterOpts provides optional per-run filtering settings for ScoreAll.
// All fields are optional; zero values apply no extra restriction.
type FilterOpts struct {
	// ExcludedPrefixes lists 3-digit (or longer) stock code prefixes to
	// reject before scoring.  Example: ["300","301","688","689"] removes
	// ChiNext and STAR-Market stocks (创业板 / 科创板) that require special
	// trading qualifications.
	ExcludedPrefixes []string

	// RequireVolume, when true, rejects stocks whose Volume == 0.
	// Volume == 0 during trading hours is a reliable indicator of suspension
	// (停牌) — the stock has not changed hands today.
	// Tip: set this to true when the daily alpha job runs during market hours
	// (09:30–15:00); leave it false for pre-market test runs.
	RequireVolume bool
}

// ── Public API ────────────────────────────────────────────────────────────────

// ScoreAll computes alpha scores for all valid stocks and returns them sorted
// best-first.  It applies the Layer 1 hard filter first.
// An optional FilterOpts can restrict boards or exclude suspended stocks.
func ScoreAll(stocks []StockInfo, opts ...FilterOpts) []ScoredStock {
	var opt FilterOpts
	if len(opts) > 0 {
		opt = opts[0]
	}
	candidates := layer1Filter(stocks, opt)
	if len(candidates) == 0 {
		return nil
	}
	scored := computeScores(candidates)
	sortDesc(scored)
	for i := range scored {
		scored[i].Rank = i + 1
	}
	return scored
}

// FilterLayer2 selects the top-n stocks after applying more restrictive
// filters.  Call this on the top-200 output of ScoreAll to get the final
// 20–50 trade candidates.
func FilterLayer2(scored []ScoredStock, topN int) []ScoredStock {
	var out []ScoredStock
	for _, s := range scored {
		if !layer2Pass(s.StockInfo) {
			continue
		}
		out = append(out, s)
		if len(out) >= topN {
			break
		}
	}
	return out
}

// ── Layer 1 – basic validity filter ──────────────────────────────────────────

func layer1Filter(stocks []StockInfo, opt FilterOpts) []StockInfo {
	out := make([]StockInfo, 0, len(stocks))
	for _, s := range stocks {
		if s.Price < minPrice {
			continue
		}
		if s.MktCap > 0 && s.MktCap < minMktCap {
			continue
		}
		if s.Turnover < minTurnover {
			continue
		}
		if isST(s.Name) {
			continue
		}
		// ── Board restriction filter ─────────────────────────────────────
		// Reject stocks on boards that require special trading qualifications
		// (e.g. ChiNext 创业板 300/301, STAR Market 科创板 688/689).
		if isExcludedBoard(s.Symbol, opt.ExcludedPrefixes) {
			continue
		}
		// ── Suspension filter ────────────────────────────────────────────
		// During trading hours a Volume of 0 means the stock has not traded
		// at all today → it is suspended (停牌) and cannot be bought.
		if opt.RequireVolume && s.Volume == 0 {
			continue
		}
		out = append(out, s)
	}
	return out
}

// isExcludedBoard returns true when the stock code starts with any prefix in
// the exclusion list.  prefixes should be 3-digit strings like "300", "688".
func isExcludedBoard(symbol string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(symbol, p) {
			return true
		}
	}
	return false
}

func isST(name string) bool {
	n := strings.ToUpper(name)
	return strings.Contains(n, "ST") || strings.Contains(n, "退")
}

// ── Layer 2 – tighter filter ──────────────────────────────────────────────────

func layer2Pass(s StockInfo) bool {
	if s.Price < l2MinPrice {
		return false
	}
	if s.MktCap > 0 {
		if s.MktCap < l2MinMktCap || s.MktCap > l2MaxMktCap {
			return false
		}
	}
	if s.Turnover < l2MinTurnover || s.Turnover > l2MaxTurnover {
		return false
	}
	return true
}

// ── Scoring ───────────────────────────────────────────────────────────────────

func computeScores(stocks []StockInfo) []ScoredStock {
	n := len(stocks)

	ret5d := extractF(stocks, func(s StockInfo) float64 { return s.Ret5d })
	ret20d := extractF(stocks, func(s StockInfo) float64 { return s.Ret20d })
	vr := extractF(stocks, func(s StockInfo) float64 { return s.VolumeRatio })
	to := extractF(stocks, func(s StockInfo) float64 { return s.Turnover })
	cp := extractF(stocks, func(s StockInfo) float64 { return s.ChangeP })

	zRet5d := zScore(ret5d)
	zRet20d := zScore(ret20d)
	zVR := zScore(vr)
	zTO := zScore(to)
	zCP := zScore(cp)

	out := make([]ScoredStock, n)
	for i, s := range stocks {
		out[i] = ScoredStock{
			StockInfo: s,
			Score: wRet5d*zRet5d[i] +
				wRet20d*zRet20d[i] +
				wVolumeRatio*zVR[i] +
				wTurnover*zTO[i] +
				wChangeP*zCP[i],
		}
	}
	return out
}

// extractF pulls one float64 field from each stock.
func extractF(stocks []StockInfo, fn func(StockInfo) float64) []float64 {
	v := make([]float64, len(stocks))
	for i, s := range stocks {
		v[i] = fn(s)
	}
	return v
}

// zScore standardises a slice to zero mean / unit variance and clamps to [-3,3].
func zScore(v []float64) []float64 {
	if len(v) == 0 {
		return nil
	}
	mean, std := meanStd(v)
	out := make([]float64, len(v))
	for i, x := range v {
		if std < 1e-12 {
			out[i] = 0
		} else {
			z := (x - mean) / std
			out[i] = math.Max(-3, math.Min(3, z))
		}
	}
	return out
}

func meanStd(v []float64) (float64, float64) {
	n := float64(len(v))
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	mean := sum / n
	variance := 0.0
	for _, x := range v {
		d := x - mean
		variance += d * d
	}
	return mean, math.Sqrt(variance / n)
}

// sortDesc sorts scored stocks by Score descending (in-place).
func sortDesc(s []ScoredStock) {
	// simple insertion sort – fast enough for ≤ 5000 elements
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j].Score < key.Score {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}
