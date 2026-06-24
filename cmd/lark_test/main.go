// cmd/lark_test/main.go — 一次性 Lark webhook 测试工具
//
// 使用方法：
//
//	LARK_WEBHOOK_URL=https://... LARK_WEBHOOK_URL2=https://... go run ./cmd/lark_test/
//
// 会依次发送三条测试消息：
//  1. 模拟买入成交通知
//  2. 模拟卖出成交通知
//  3. 模拟当日平仓日报（含账户汇总）
package main

import (
	"context"
	"log"
	"os"
	"time"

	"astock_trade/core"
	"astock_trade/notify/lark"
)

func main() {
	client := lark.New()
	if client == nil {
		log.Fatal("❌ 未配置 LARK_WEBHOOK_URL，请先设置环境变量。\n" +
			"示例：LARK_WEBHOOK_URL=https://open.larksuite.com/open-apis/bot/v2/hook/xxx go run ./cmd/lark_test/")
	}

	log.Println("开始发送 Lark 测试消息…")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cst := time.FixedZone("CST", 8*3600)
	now := time.Now().In(cst)

	// ── 1. 买入通知 ──────────────────────────────────────────────────────────
	buyTrade := &core.Trade{
		Symbol:     "603773",
		Side:       "BUY",
		Price:      136.12,
		OrderPrice: 135.85,
		Quantity:   100,
		Timestamp:  now.UnixMilli(),
	}
	if err := client.SendTradeAlert(ctx, buyTrade); err != nil {
		log.Printf("❌ 买入通知发送失败: %v", err)
	} else {
		log.Println("✅ 买入通知已发送")
	}

	time.Sleep(500 * time.Millisecond) // 避免触发飞书限流

	// ── 2. 卖出通知 ──────────────────────────────────────────────────────────
	sellTrade := &core.Trade{
		Symbol:     "603773",
		Side:       "SELL",
		Price:      147.05,
		OrderPrice: 147.00,
		Quantity:   100,
		Timestamp:  now.UnixMilli(),
	}
	if err := client.SendTradeAlert(ctx, sellTrade); err != nil {
		log.Printf("❌ 卖出通知发送失败: %v", err)
	} else {
		log.Println("✅ 卖出通知已发送")
	}

	time.Sleep(500 * time.Millisecond)

	// ── 3. 平仓日报 ──────────────────────────────────────────────────────────
	openTime := time.Date(now.Year(), now.Month(), now.Day(), 9, 54, 0, 0, cst).UnixMilli()
	closeTime := now.UnixMilli()

	closedTrades := []core.ClosedTrade{
		{
			Symbol:     "603773",
			EntryPrice: 135.85,
			ExitPrice:  147.00,
			Quantity:   100,
			PnlPct:     8.21,
			OpenTime:   openTime,
			CloseTime:  closeTime,
		},
		{
			Symbol:     "000725",
			EntryPrice: 6.55,
			ExitPrice:  6.18,
			Quantity:   1000,
			PnlPct:     -5.65,
			OpenTime:   openTime,
			CloseTime:  closeTime,
		},
	}

	summary := lark.CloseReportSummary{
		Cash:          88_345.67,
		PositionValue: 32_150.00,
		TotalEquity:   120_495.67,
		TodayClosePnl: lark.PnlAbs(closedTrades[0]) + lark.PnlAbs(closedTrades[1]),
	}

	dateStr := now.Format("2006-01-02")
	if err := client.SendDailyCloseReport(ctx, dateStr, closedTrades, summary); err != nil {
		log.Printf("❌ 平仓日报发送失败: %v", err)
	} else {
		log.Println("✅ 平仓日报已发送")
	}

	// 检查配置了几个 bot
	count := 0
	for _, env := range []string{"LARK_WEBHOOK_URL", "LARK_WEBHOOK_URL2"} {
		if os.Getenv(env) != "" {
			count++
		}
	}
	log.Printf("共 %d 个 webhook 收到消息，请检查飞书/Lark 群", count)
}
