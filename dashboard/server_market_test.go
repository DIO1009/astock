package dashboard

import (
	"testing"

	"astock_trade/core"
)

func TestDashboardMarketInfoUsesShanghaiCompositeLabel(t *testing.T) {
	srv := &Server{}
	got := srv.buildMarket(map[string]*core.Quote{
		"000001.SH": {Symbol: "000001.SH", Price: 3123.45},
		"000300":    {Symbol: "000300", Price: 3999.99},
	})
	if got.IndexName != "上证指数" {
		t.Fatalf("IndexName = %q, want 上证指数", got.IndexName)
	}
	if got.IndexPrice != 3123.45 {
		t.Fatalf("IndexPrice = %v, want 3123.45", got.IndexPrice)
	}

	got = srv.buildMarket(nil)
	if got.IndexName != "上证指数" {
		t.Fatalf("IndexName = %q, want 上证指数", got.IndexName)
	}
}
