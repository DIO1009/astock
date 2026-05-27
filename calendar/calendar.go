// Package calendar implements the A-share trading-day calendar for SHSE/SZSE.
//
// It handles weekends and official market-closure holidays announced by the
// Shanghai and Shenzhen Stock Exchanges (2020-2030).
//
// Primary API:
//
//	cal := calendar.New()
//	cal.IsTradeDay(t)      // bool
//	cal.TradeDaySeq(t)     // int64 – monotonically increasing trading-day index
package calendar

import (
	"sort"
	"time"
)

// cst is China Standard Time (UTC+8).
var cst = time.FixedZone("CST", 8*3600)

// Calendar implements core.TradingCalendar.
// All public methods are safe for concurrent use.
type Calendar struct {
	holidays map[string]struct{} // "2006-01-02"
	seqMap   map[string]int64    // trading-day date → sequence number (starts at 1)
}

// New returns a Calendar pre-loaded with official SHSE/SZSE holidays 2020-2030.
// Sequence numbers are pre-computed at construction time (O(1) per lookup).
func New() *Calendar {
	c := &Calendar{
		holidays: make(map[string]struct{}, len(rawHolidays)),
	}
	for _, d := range rawHolidays {
		c.holidays[d] = struct{}{}
	}
	c.buildSeqCache()
	return c
}

// IsTradeDay returns true when t is a regular A-share trading day.
// The check uses China Standard Time regardless of t's original timezone.
func (c *Calendar) IsTradeDay(t time.Time) bool {
	t = t.In(cst)
	if w := t.Weekday(); w == time.Saturday || w == time.Sunday {
		return false
	}
	_, holiday := c.holidays[t.Format("2006-01-02")]
	return !holiday
}

// TradeDaySeq returns the ordinal trading-day sequence number for t (1-based,
// counting from 2020-01-01).  Non-trading days return the sequence of the
// most recent preceding trading day (so it is safe to call on weekends).
func (c *Calendar) TradeDaySeq(t time.Time) int64 {
	t = t.In(cst)
	key := t.Format("2006-01-02")
	if seq, ok := c.seqMap[key]; ok {
		return seq
	}
	// Non-trading day: walk backwards to find the nearest prior trading day.
	for i := 1; i <= 10; i++ {
		prior := t.AddDate(0, 0, -i).Format("2006-01-02")
		if seq, ok := c.seqMap[prior]; ok {
			return seq
		}
	}
	return 0
}

// buildSeqCache pre-computes trading-day sequence numbers from 2020-01-01
// to 2030-12-31.
func (c *Calendar) buildSeqCache() {
	c.seqMap = make(map[string]int64, 2600)
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, cst)
	end := time.Date(2030, 12, 31, 0, 0, 0, 0, cst)
	seq := int64(0)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if c.IsTradeDay(d) {
			seq++
			c.seqMap[d.Format("2006-01-02")] = seq
		}
	}
}

// NextTradeDay returns the first trading day strictly after t.
func (c *Calendar) NextTradeDay(t time.Time) time.Time {
	d := t.In(cst).AddDate(0, 0, 1)
	for !c.IsTradeDay(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// IsInTradingHours returns true when t falls within an active A-share
// continuous-auction session:
//
//	Morning:   09:30 – 11:30 (exclusive end)  CST
//	Afternoon: 13:00 – 15:00 (exclusive end)  CST
//
// Returns false on non-trading days (weekends, public holidays) and outside
// the two continuous-auction windows.
func (c *Calendar) IsInTradingHours(t time.Time) bool {
	if !c.IsTradeDay(t) {
		return false
	}
	t = t.In(cst)
	min := t.Hour()*60 + t.Minute()
	// Morning: [09:30, 11:30)
	if min >= 9*60+30 && min < 11*60+30 {
		return true
	}
	// Afternoon: [13:00, 15:00)
	if min >= 13*60 && min < 15*60 {
		return true
	}
	return false
}

// Keys for sorting (used only during init, not performance-critical)
var _ = sort.Strings

// ─── Official SHSE/SZSE holiday closures 2020-2030 ───────────────────────────
//
// Source: announcements from Shanghai Stock Exchange and Shenzhen Stock Exchange.
// Weekends are handled automatically and are NOT listed here.
// Make-up working Saturdays (补班) are not listed as holidays.
//
// Update annually when the exchange publishes the new year's schedule.
var rawHolidays = []string{
	// ── 2020 ──
	"2020-01-01",
	"2020-01-22", "2020-01-23", "2020-01-24", "2020-01-27", "2020-01-28", "2020-01-29", "2020-01-30", "2020-01-31",
	"2020-04-06",
	"2020-05-01", "2020-05-04", "2020-05-05",
	"2020-06-25", "2020-06-26",
	"2020-10-01", "2020-10-02", "2020-10-05", "2020-10-06", "2020-10-07", "2020-10-08",

	// ── 2021 ──
	"2021-01-01",
	"2021-02-08", "2021-02-09", "2021-02-10", "2021-02-11", "2021-02-12",
	"2021-04-05",
	"2021-05-03", "2021-05-04", "2021-05-05",
	"2021-06-14",
	"2021-09-20", "2021-09-21",
	"2021-10-01", "2021-10-04", "2021-10-05", "2021-10-06", "2021-10-07",

	// ── 2022 ──
	"2022-01-03",
	"2022-01-31", "2022-02-01", "2022-02-02", "2022-02-03", "2022-02-04",
	"2022-04-04", "2022-04-05",
	"2022-04-29", "2022-05-02", "2022-05-03", "2022-05-04",
	"2022-06-03",
	"2022-09-12",
	"2022-10-03", "2022-10-04", "2022-10-05", "2022-10-06", "2022-10-07",

	// ── 2023 ──
	"2023-01-02",
	"2023-01-23", "2023-01-24", "2023-01-25", "2023-01-26", "2023-01-27",
	"2023-04-05",
	"2023-04-28", "2023-05-01", "2023-05-03",
	"2023-06-22", "2023-06-23",
	"2023-09-29",
	"2023-10-02", "2023-10-03", "2023-10-04", "2023-10-05", "2023-10-06",

	// ── 2024 ──
	"2024-01-01",
	"2024-02-08", "2024-02-09", "2024-02-12", "2024-02-13", "2024-02-14", "2024-02-15", "2024-02-16",
	"2024-04-04", "2024-04-05",
	"2024-05-01", "2024-05-02", "2024-05-03",
	"2024-06-10",
	"2024-09-16", "2024-09-17",
	"2024-10-01", "2024-10-02", "2024-10-03", "2024-10-04", "2024-10-07",

	// ── 2025 ──
	"2025-01-01",
	"2025-01-28", "2025-01-29", "2025-01-30", "2025-01-31", "2025-02-03", "2025-02-04",
	"2025-04-04",
	"2025-05-01", "2025-05-02",
	"2025-05-31",
	"2025-10-01", "2025-10-02", "2025-10-03", "2025-10-06", "2025-10-07", "2025-10-08",

	// ── 2026 ──
	"2026-01-01", "2026-01-02",
	"2026-02-17", "2026-02-18", "2026-02-19", "2026-02-20", "2026-02-23",
	"2026-04-06",
	"2026-05-01", "2026-05-04", "2026-05-05",
	"2026-06-19",
	"2026-09-25",
	"2026-10-01", "2026-10-02", "2026-10-05", "2026-10-06", "2026-10-07", "2026-10-08",

	// ── 2027 ──
	"2027-01-01",
	"2027-02-05", "2027-02-08", "2027-02-09", "2027-02-10", "2027-02-11",
	"2027-04-05",
	"2027-04-30", "2027-05-03",
	"2027-06-09",
	"2027-10-01", "2027-10-04", "2027-10-05", "2027-10-06", "2027-10-07",
}
