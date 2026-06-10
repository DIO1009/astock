package lark

import (
	"strings"
	"testing"
	"time"

	"astock_trade/core"
)

func TestPnlAbs(t *testing.T) {
	t.Parallel()
	win := core.ClosedTrade{EntryPrice: 100, ExitPrice: 110, Quantity: 500}
	if got := PnlAbs(win); got != 5000 {
		t.Fatalf("PnlAbs win = %v, want 5000", got)
	}
	loss := core.ClosedTrade{EntryPrice: 50, ExitPrice: 47, Quantity: 200}
	if got := PnlAbs(loss); got != -600 {
		t.Fatalf("PnlAbs loss = %v, want -600", got)
	}
}

func TestFormatMoney(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   float64
		want string
	}{
		{1234.56, "+¥1,234.56"},
		{-800, "-¥800.00"},
		{0, "+¥0.00"},
		{1000000.5, "+¥1,000,000.50"},
	}
	for _, tc := range cases {
		if got := formatMoney(tc.in); got != tc.want {
			t.Errorf("formatMoney(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatMoneyPlain(t *testing.T) {
	t.Parallel()
	if got := formatMoneyPlain(9876.5); got != "¥9,876.50" {
		t.Fatalf("formatMoneyPlain = %q", got)
	}
}

func TestBuildTradeBlock(t *testing.T) {
	t.Parallel()
	open := time.Date(2025, 6, 9, 9, 54, 0, 0, cst).UnixMilli()
	close := time.Date(2025, 6, 10, 9, 35, 0, 0, cst).UnixMilli()
	ct := core.ClosedTrade{
		Symbol:     "603773",
		EntryPrice: 135.85,
		ExitPrice:  147.00,
		Quantity:   100,
		PnlPct:     8.08,
		OpenTime:   open,
		CloseTime:  close,
	}
	block := buildTradeBlock(ct)
	for _, want := range []string{
		"**603773**",
		"开仓 135.85 → 平仓 147.00",
		"收益率 +8.08%",
		"收益额 +¥1,115.00",
		"06-09 09:54 → 06-10 09:35",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("buildTradeBlock missing %q in:\n%s", want, block)
		}
	}
}

func TestBuildCloseReportElements(t *testing.T) {
	t.Parallel()
	summary := CloseReportSummary{
		Cash:          50000,
		PositionValue: 30000,
		TotalEquity:   80000,
		TodayClosePnl: 1500,
	}

	empty := buildCloseReportElements(nil, summary)
	if len(empty) != 1 {
		t.Fatalf("0 trades: len(elements)=%d, want 1 summary block", len(empty))
	}

	trades := []core.ClosedTrade{
		{Symbol: "A", EntryPrice: 10, ExitPrice: 11, Quantity: 100},
		{Symbol: "B", EntryPrice: 20, ExitPrice: 19, Quantity: 50},
	}
	elems := buildCloseReportElements(trades, summary)
	// 2 trade divs + 1 hr between + 1 hr before summary + 1 summary = 5
	wantLen := 5
	if len(elems) != wantLen {
		t.Fatalf("2 trades: len(elements)=%d, want %d", len(elems), wantLen)
	}
	if tag, _ := elems[1].(map[string]interface{})["tag"].(string); tag != "hr" {
		t.Fatalf("element[1] tag=%q, want hr", tag)
	}
	if tag, _ := elems[3].(map[string]interface{})["tag"].(string); tag != "hr" {
		t.Fatalf("element[3] tag=%q, want hr before summary", tag)
	}
	summaryElem, ok := elems[4].(map[string]interface{})
	if !ok || summaryElem["tag"] != "div" {
		t.Fatalf("element[4] should be summary div")
	}
	fields, ok := summaryElem["fields"].([]interface{})
	if !ok || len(fields) != 4 {
		t.Fatalf("summary fields len=%d, want 4", len(fields))
	}
}
