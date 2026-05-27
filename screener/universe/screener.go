// Package universe provides a Screener backed by a fixed 20-symbol A-share universe
// grouped into six sectors.  The universe represents a diversified set of blue-chip
// and growth stocks suitable for portfolio-level stress testing.
//
// Sectors:
//
//	CONSUMER    (4): 600519 000858 600887 601888
//	TECH        (4): 300750 002415 000063 600588
//	FINANCE     (4): 600036 601318 601398 601166
//	ENERGY      (4): 600900 601985 600028 601088
//	HEALTHCARE  (2): 600276 000538
//	INDUSTRIAL  (2): 000651 601238
package universe

import "astock_trade/core"

// Sector names used in SectorOf.
const (
	SectorConsumer   = "CONSUMER"
	SectorTech       = "TECH"
	SectorFinance    = "FINANCE"
	SectorEnergy     = "ENERGY"
	SectorHealthcare = "HEALTHCARE"
	SectorIndustrial = "INDUSTRIAL"
)

// Symbols20 is the canonical 20-symbol universe, ordered by sector.
var Symbols20 = []string{
	"600519", "000858", "600887", "601888", // CONSUMER
	"300750", "002415", "000063", "600588", // TECH
	"600036", "601318", "601398", "601166", // FINANCE
	"600900", "601985", "600028", "601088", // ENERGY
	"600276", "000538",                     // HEALTHCARE
	"000651", "601238",                     // INDUSTRIAL
}

// SectorOf maps each symbol in Symbols20 to its sector name.
var SectorOf = map[string]string{
	"600519": SectorConsumer, "000858": SectorConsumer,
	"600887": SectorConsumer, "601888": SectorConsumer,
	"300750": SectorTech, "002415": SectorTech,
	"000063": SectorTech, "600588": SectorTech,
	"600036": SectorFinance, "601318": SectorFinance,
	"601398": SectorFinance, "601166": SectorFinance,
	"600900": SectorEnergy, "601985": SectorEnergy,
	"600028": SectorEnergy, "601088": SectorEnergy,
	"600276": SectorHealthcare, "000538": SectorHealthcare,
	"000651": SectorIndustrial, "601238": SectorIndustrial,
}

// Screener satisfies core.Screener for the 20-symbol universe.
type Screener struct {
	symbols []string
}

// New returns a Screener for the provided symbol list.
// Pass nil or an empty slice to use the default Symbols20.
func New(symbols []string) *Screener {
	if len(symbols) == 0 {
		symbols = Symbols20
	}
	return &Screener{symbols: symbols}
}

// Screen returns the configured symbol list. Satisfies core.Screener.
func (s *Screener) Screen() []string { return s.symbols }

// Compile-time interface check.
var _ core.Screener = (*Screener)(nil)
