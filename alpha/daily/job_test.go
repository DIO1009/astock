package daily

import (
	"testing"
	"time"
)

func TestDefaultConfigUsesTop100FinalLimit(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.TopLayer1 != 200 {
		t.Fatalf("TopLayer1 = %d, want 200", cfg.TopLayer1)
	}
	if cfg.TopLayer2 != 100 {
		t.Fatalf("TopLayer2 = %d, want 100", cfg.TopLayer2)
	}
	if cfg.ScanTimeoutSecs != 300 {
		t.Fatalf("ScanTimeoutSecs = %d, want 300", cfg.ScanTimeoutSecs)
	}
}

func TestResolveRunDateStillUsesConfiguredTradeDate(t *testing.T) {
	tradeDate := time.Date(2026, 5, 25, 14, 30, 45, 123, time.UTC)
	now := time.Date(2026, 5, 26, 9, 15, 0, 0, time.UTC)
	got := resolveRunDate(now, Config{TradeDate: tradeDate})
	wantDate := tradeDate.In(cst).Format("2006-01-02")

	if got.Format("2006-01-02") != wantDate {
		t.Fatalf("resolveRunDate date = %s, want %s", got.Format("2006-01-02"), wantDate)
	}
	if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
		t.Fatalf("resolveRunDate did not normalize to midnight: %s", got.Format(time.RFC3339Nano))
	}
}
