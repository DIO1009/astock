package universe

import (
	"encoding/json"
	"math"
	"testing"
)

func num(s string) json.Number { return json.Number(s) }

func TestStockInfoFromRawUsesF109F110F160ForReturns(t *testing.T) {
	raw := rawStock{
		F12:  "600503",
		F14:  "华丽家族",
		F13:  num("1"),
		F2:   num("2.60"),
		F3:   num("10.17"),
		F5:   num("123"),
		F6:   num("456.7"),
		F8:   num("3.21"),
		F9:   num("12.3"),
		F10:  num("1.4"),
		F20:  num("10000000000"),
		F21:  num("8000000000"),
		F23:  num("1.2"),
		F109: num("5.26"),
		F110: num("13.04"),
		F160: num("1.17"),
	}

	stock, ok := stockInfoFromRaw(raw)
	if !ok {
		t.Fatalf("stockInfoFromRaw returned ok false")
	}
	if stock.Symbol != "600503" {
		t.Fatalf("Symbol = %q, want %q", stock.Symbol, "600503")
	}
	if stock.Name != "华丽家族" {
		t.Fatalf("Name = %q, want %q", stock.Name, "华丽家族")
	}
	if stock.Market != 1 {
		t.Fatalf("Market = %d, want %d", stock.Market, 1)
	}
	if !almostEqual(stock.Price, 2.60) {
		t.Fatalf("Price = %v, want %v", stock.Price, 2.60)
	}
	if stock.Volume != 12300 {
		t.Fatalf("Volume = %v, want %v", stock.Volume, int64(12300))
	}
	if !almostEqual(stock.Ret5d, 5.26) {
		t.Fatalf("Ret5d = %v, want %v", stock.Ret5d, 5.26)
	}
	if !almostEqual(stock.Ret10d, 13.04) {
		t.Fatalf("Ret10d = %v, want %v", stock.Ret10d, 13.04)
	}
	if !almostEqual(stock.Ret20d, 1.17) {
		t.Fatalf("Ret20d = %v, want %v", stock.Ret20d, 1.17)
	}
}

func TestToReturnRejectsImpossibleAndListingDateLikeValues(t *testing.T) {
	tests := []struct {
		value json.Number
		want  float64
	}{
		{num("5.26"), 5.26},
		{num("-100"), -100},
		{num("-100.01"), 0},
		{num("1000"), 1000},
		{num("1000.01"), 0},
		{num("20020709"), 0},
		{num("-2147483648"), 0},
		{num("-"), 0},
		{num(""), 0},
	}

	for _, tt := range tests {
		got := toReturn(tt.value)
		if !almostEqual(got, tt.want) {
			t.Fatalf("toReturn(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestStockInfoFromRawRejectsInvalidRows(t *testing.T) {
	if _, ok := stockInfoFromRaw(rawStock{F12: "", F2: num("10")}); ok {
		t.Fatalf("stockInfoFromRaw accepted empty symbol")
	}
	if _, ok := stockInfoFromRaw(rawStock{F12: "12345", F2: num("10")}); ok {
		t.Fatalf("stockInfoFromRaw accepted invalid symbol length")
	}
	if _, ok := stockInfoFromRaw(rawStock{F12: "600503", F2: num("0")}); ok {
		t.Fatalf("stockInfoFromRaw accepted zero price")
	}
}

func almostEqual(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9
}
