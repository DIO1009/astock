// Package regime provides a daily-frequency market regime classifier.
//
// Unlike the tick-level SMA-deviation trend filter (market/trend), this
// classifier uses daily returns (Return5d/Return20d from the index Quote) to
// determine the broad market regime. It serves as input to factor weight gating
// and entry confirmation (via stabilizer).
//
// Classification thresholds (loose, daily-frequency):
//
//	UPTREND:   Return5d > +1%  AND  Return20d > 0%
//	DOWNTREND: Return5d < -1%  OR   Return20d < -2%
//	OSCILLATE: everything else
package regime

import "astock_trade/core"

// Classifier allows the engine to inject daily regime classification.
type Classifier interface {
	Classify(indexQuote *core.Quote) core.MarketState
}

// DailyRegime implements Classifier using Return5d/Return20d from the index quote.
type DailyRegime struct{}

// New returns a stateless DailyRegime classifier.
func New() *DailyRegime {
	return &DailyRegime{}
}

// Classify determines the market regime from daily index returns.
// Returns MarketOscillate when indexQuote is nil.
func (d *DailyRegime) Classify(indexQuote *core.Quote) core.MarketState {
	if indexQuote == nil {
		return core.MarketOscillate
	}
	ret5d := indexQuote.Return5d
	ret20d := indexQuote.Return20d

	// UPTREND: 5d > 1% AND 20d > 0%
	if ret5d > 1.0 && ret20d > 0 {
		return core.MarketUptrend
	}
	// DOWNTREND: 5d < -1% OR 20d < -2%
	if ret5d < -1.0 || ret20d < -2.0 {
		return core.MarketDowntrend
	}
	return core.MarketOscillate
}
