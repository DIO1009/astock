package realistic

import (
	"math"
	"testing"
)

func assertClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.10f, want %.10f", name, got, want)
	}
}

func TestCalculateTradingFeeBuyUsesMinCommissionAndTransferFee(t *testing.T) {
	cfg := Config{CommissionPct: 0.000235, StampTaxPct: 0.0005, TransferFeePct: 0.00001, MinCommission: 5}
	amount := 49881.0

	commission, stampTax, transferFee, totalFee, err := calculateTradingFee("BUY", amount, cfg)
	if err != nil {
		t.Fatalf("calculateTradingFee returned error: %v", err)
	}

	assertClose(t, "commission", commission, 11.722035)
	assertClose(t, "stampTax", stampTax, 0)
	assertClose(t, "transferFee", transferFee, 0.49881)
	assertClose(t, "totalFee", totalFee, 12.220845)
}

func TestCalculateTradingFeeSellIncludesStampTax(t *testing.T) {
	cfg := Config{CommissionPct: 0.000235, StampTaxPct: 0.0005, TransferFeePct: 0.00001, MinCommission: 5}
	amount := 87150.0

	commission, stampTax, transferFee, totalFee, err := calculateTradingFee("SELL", amount, cfg)
	if err != nil {
		t.Fatalf("calculateTradingFee returned error: %v", err)
	}

	assertClose(t, "commission", commission, 20.48025)
	assertClose(t, "stampTax", stampTax, 43.575)
	assertClose(t, "transferFee", transferFee, 0.8715)
	assertClose(t, "totalFee", totalFee, 64.92675)
}

func TestCalculateTradingFeeAppliesMinimumCommission(t *testing.T) {
	cfg := Config{CommissionPct: 0.000235, StampTaxPct: 0.0005, TransferFeePct: 0.00001, MinCommission: 5}
	amount := 3000.0

	buyCommission, buyStampTax, buyTransferFee, buyTotalFee, err := calculateTradingFee("BUY", amount, cfg)
	if err != nil {
		t.Fatalf("calculateTradingFee BUY returned error: %v", err)
	}
	assertClose(t, "buy commission", buyCommission, 5)
	assertClose(t, "buy stampTax", buyStampTax, 0)
	assertClose(t, "buy transferFee", buyTransferFee, 0.03)
	assertClose(t, "buy totalFee", buyTotalFee, 5.03)

	sellCommission, sellStampTax, sellTransferFee, sellTotalFee, err := calculateTradingFee("SELL", amount, cfg)
	if err != nil {
		t.Fatalf("calculateTradingFee SELL returned error: %v", err)
	}
	assertClose(t, "sell commission", sellCommission, 5)
	assertClose(t, "sell stampTax", sellStampTax, 1.5)
	assertClose(t, "sell transferFee", sellTransferFee, 0.03)
	assertClose(t, "sell totalFee", sellTotalFee, 6.53)
}

func TestCalculateTradingFeeRejectsUnknownSide(t *testing.T) {
	cfg := Config{CommissionPct: 0.000235, StampTaxPct: 0.0005, TransferFeePct: 0.00001, MinCommission: 5}

	_, _, _, _, err := calculateTradingFee("HOLD", 1000, cfg)
	if err == nil {
		t.Fatal("expected error for unknown side")
	}
}
