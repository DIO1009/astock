package regime

import (
	"testing"

	"astock_trade/core"
)

func TestClassifyNilQuoteReturnsOscillate(t *testing.T) {
	d := New()
	if got := d.Classify(nil); got != core.MarketOscillate {
		t.Fatalf("Classify(nil) = %s, want %s", got, core.MarketOscillate)
	}
}

func TestClassifyUptrend(t *testing.T) {
	d := New()
	// 5d = 2%, 20d = 1% → UPTREND (5d>1% AND 20d>0%)
	q := &core.Quote{Return5d: 2.0, Return20d: 1.0}
	if got := d.Classify(q); got != core.MarketUptrend {
		t.Fatalf("Classify(5d=2%%, 20d=1%%) = %s, want %s", got, core.MarketUptrend)
	}
}

func TestClassifyUptrendBoundary(t *testing.T) {
	d := New()
	// 5d = 1.0001%, 20d = 0.0001% → UPTREND (just above threshold)
	q := &core.Quote{Return5d: 1.0001, Return20d: 0.0001}
	if got := d.Classify(q); got != core.MarketUptrend {
		t.Fatalf("Classify(5d=1.0001%%, 20d=0.0001%%) = %s, want %s", got, core.MarketUptrend)
	}
}

func TestClassifyNotUptrend5dBelowThreshold(t *testing.T) {
	d := New()
	// 5d = 0.5%, 20d = 5% → OSCILLATE (5d not > 1%)
	q := &core.Quote{Return5d: 0.5, Return20d: 5.0}
	if got := d.Classify(q); got != core.MarketOscillate {
		t.Fatalf("Classify(5d=0.5%%, 20d=5%%) = %s, want %s", got, core.MarketOscillate)
	}
}

func TestClassifyNotUptrend20dZero(t *testing.T) {
	d := New()
	// 5d = 2%, 20d = 0% → OSCILLATE (20d not > 0%)
	q := &core.Quote{Return5d: 2.0, Return20d: 0.0}
	if got := d.Classify(q); got != core.MarketOscillate {
		t.Fatalf("Classify(5d=2%%, 20d=0%%) = %s, want %s", got, core.MarketOscillate)
	}
}

func TestClassifyDowntrend5dOnly(t *testing.T) {
	d := New()
	// 5d = -1.5%, 20d = 0% → DOWNTREND (5d < -1%)
	q := &core.Quote{Return5d: -1.5, Return20d: 0.0}
	if got := d.Classify(q); got != core.MarketDowntrend {
		t.Fatalf("Classify(5d=-1.5%%, 20d=0%%) = %s, want %s", got, core.MarketDowntrend)
	}
}

func TestClassifyDowntrend20dOnly(t *testing.T) {
	d := New()
	// 5d = 0%, 20d = -3% → DOWNTREND (20d < -2%)
	q := &core.Quote{Return5d: 0.0, Return20d: -3.0}
	if got := d.Classify(q); got != core.MarketDowntrend {
		t.Fatalf("Classify(5d=0%%, 20d=-3%%) = %s, want %s", got, core.MarketDowntrend)
	}
}

func TestClassifyOscillateModerateGains(t *testing.T) {
	d := New()
	// 5d = 0.8%, 20d = -0.5% → OSCILLATE (not uptrend, not downtrend)
	q := &core.Quote{Return5d: 0.8, Return20d: -0.5}
	if got := d.Classify(q); got != core.MarketOscillate {
		t.Fatalf("Classify(5d=0.8%%, 20d=-0.5%%) = %s, want %s", got, core.MarketOscillate)
	}
}

func TestClassifyOscillateMixed(t *testing.T) {
	d := New()
	// 5d = 1.5% but 20d = -1% → OSCILLATE (20d not > 0 for uptrend, not < -2 for downtrend)
	q := &core.Quote{Return5d: 1.5, Return20d: -1.0}
	if got := d.Classify(q); got != core.MarketOscillate {
		t.Fatalf("Classify(5d=1.5%%, 20d=-1%%) = %s, want %s", got, core.MarketOscillate)
	}
}
