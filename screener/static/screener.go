// Package static provides a Screener that returns a fixed symbol universe.
// Swap this implementation for a dynamic screener (fundamental filters,
// technical criteria, ML ranking) without changing any other package.
package static

// Screener satisfies core.Screener with a compile-time symbol list.
type Screener struct {
	symbols []string
}

// New returns a Screener backed by the provided symbol slice.
func New(symbols []string) *Screener {
	cp := make([]string, len(symbols))
	copy(cp, symbols)
	return &Screener{symbols: cp}
}

// Screen returns the fixed watch-list.
// TODO: replace with a live screener (e.g. top-N by turnover rate, MACD signal, etc.)
func (s *Screener) Screen() []string {
	out := make([]string, len(s.symbols))
	copy(out, s.symbols)
	return out
}
