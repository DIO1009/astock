package main

import (
	"context"
	"log"
	"os"
	"time"

	"astock_trade/alpha/daily"
	"astock_trade/calendar"
	"astock_trade/core"
	"astock_trade/provider/eastmoney"
	"astock_trade/rotation"
	"astock_trade/screener/dynamic"
	"astock_trade/store"
)

// ── 每日选股调度器 ─────────────────────────────────────────────────────────────

var alphaCST = time.FixedZone("CST", 8*3600)

type initialAlphaRunPlan struct {
	ShouldRun    bool
	SkipNonTrade bool
	TradeDate    time.Time
	WaitUntil    time.Time
}

func alphaTradeDate(t time.Time, loc *time.Location) time.Time {
	day := t.In(loc)
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
}

func planInitialAlphaRun(latestDate time.Time, latestErr error, now time.Time, loc *time.Location, cal core.TradingCalendar) initialAlphaRunPlan {
	now = now.In(loc)
	tradeDate := alphaTradeDate(now, loc)
	if cal != nil && !cal.IsTradeDay(now) {
		return initialAlphaRunPlan{SkipNonTrade: true, TradeDate: tradeDate}
	}
	if latestErr == nil && !latestDate.IsZero() && !alphaTradeDate(latestDate, loc).Before(tradeDate) {
		return initialAlphaRunPlan{TradeDate: tradeDate}
	}

	plan := initialAlphaRunPlan{ShouldRun: true, TradeDate: tradeDate}
	marketOpen := time.Date(now.Year(), now.Month(), now.Day(), 9, 30, 0, 0, loc)
	if now.Before(marketOpen) {
		plan.WaitUntil = marketOpen
	}
	return plan
}

// runAlphaScheduler runs the daily alpha job automatically:
//   - On startup: runs immediately if today's DB data is missing (waits until
//     09:30 CST if market has not yet opened so the clist API has live data).
//   - Every day: wakes at alphaRunHour:alphaRunMin (China Standard Time) and
//     re-runs the full universe scan with retry, then signals the screener to refresh.
func runAlphaScheduler(ctx context.Context, st *store.Store, sc *dynamic.Screener, dp core.DataProvider) {
	cal := calendar.New()

	// ① 启动时：若交易日排名数据缺失，等到 09:30 后再跑（开盘前 clist API 返回空数据）
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	latestDate, err := st.GetLatestRankingDate(checkCtx)
	cancel()

	plan := planInitialAlphaRun(latestDate, err, time.Now().In(alphaCST), alphaCST, cal)
	if plan.SkipNonTrade {
		log.Printf("[Scheduler] 今日非交易日（%s），跳过初始选股", plan.TradeDate.Format("2006-01-02"))
	} else if plan.ShouldRun {
		if !plan.WaitUntil.IsZero() {
			waitDur := time.Until(plan.WaitUntil)
			log.Printf("[Scheduler] 当前 %s 早于开盘，等待 %s 后启动初始选股…",
				time.Now().In(alphaCST).Format("15:04:05"), waitDur.Round(time.Second))
			select {
			case <-ctx.Done():
				return
			case <-time.After(waitDur):
			}
		}
		log.Println("[Scheduler] DB 无今日 alpha 数据，开始初始选股…")
		runAndRefreshWithRetry(ctx, st, sc, dp, plan.TradeDate)
	} else {
		log.Printf("[Scheduler] DB 已有今日排名（%s），跳过初始选股", latestDate.In(alphaCST).Format("2006-01-02"))
	}

	// ② 每个交易日 09:31 定时运行（开盘后数据稳定）
	for {
		next := nextAlphaRunTimeAfter(time.Now().In(alphaCST), alphaRunHour, alphaRunMin, alphaCST, cal)
		log.Printf("[Scheduler] 下次选股时间: %s", next.Format("2006-01-02 15:04:05 CST"))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			log.Printf("[Scheduler] ⏰ %02d:%02d 到达，开始每日全市场选股…", alphaRunHour, alphaRunMin)
			runAndRefreshWithRetry(ctx, st, sc, dp, alphaTradeDate(next, alphaCST))
		}
	}
}


// alphaRunHour / alphaRunMin: 每日选股触发时刻（北京时间）。
// 09:31 确保开盘第一分钟行情数据已就绪，避免 clist API 返回空数据。
const (
	alphaRunHour = 9
	alphaRunMin  = 31
)


// alphaRetryMax / alphaRetryInterval: 选股失败时的重试策略。
const (
	alphaRetryMax      = 6
	alphaRetryInterval = 30 * time.Second
)


// runAndRefreshWithRetry 在选股失败时按固定间隔重试，最多 alphaRetryMax 次。
func runAndRefreshWithRetry(ctx context.Context, st *store.Store, sc *dynamic.Screener, dp core.DataProvider, tradeDate time.Time) {
	for i := 0; i <= alphaRetryMax; i++ {
		if i > 0 {
			log.Printf("[Scheduler] ⏳ 第 %d/%d 次重试，等待 %s…", i, alphaRetryMax, alphaRetryInterval)
			select {
			case <-ctx.Done():
				return
			case <-time.After(alphaRetryInterval):
			}
		}
		if runAndRefresh(ctx, st, sc, dp, tradeDate) {
			return
		}
	}
	log.Printf("[Scheduler] ❌ 已重试 %d 次仍失败，今日选股放弃", alphaRetryMax)
}


// runAndRefresh executes the daily alpha job and refreshes the screener cache.
// Returns true on success, false on failure (caller may retry).
func runAndRefresh(ctx context.Context, st *store.Store, sc *dynamic.Screener, dp core.DataProvider, tradeDate time.Time) bool {
	// Default excluded board prefixes: 创业板 (300/301) and 科创板 (688/689).
	// These boards require special investor qualifications that most retail
	// traders do not have.  Override with ASTOCK_INCLUDE_ALL_BOARDS=1 to
	// disable this restriction (e.g. if you DO have the qualifications).
	excludedPrefixes := []string{"300", "301", "688", "689"}
	if os.Getenv("ASTOCK_INCLUDE_ALL_BOARDS") == "1" {
		excludedPrefixes = nil
		log.Println("[Scheduler] ⚠️  ASTOCK_INCLUDE_ALL_BOARDS=1: 已启用创业板/科创板（确认你有交易资格）")
	}

	cfg := daily.Config{
		TopLayer1:        envInt("ASTOCK_TOP_LAYER1", 200),
		TopLayer2:        envInt("ASTOCK_TOP_N", 100),
		ScanTimeoutSecs:  envInt("ASTOCK_SCAN_TIMEOUT", 300),
		ExcludedPrefixes: excludedPrefixes,
		RequireVolume:    true, // reject suspended stocks (Volume==0 during trading hours)
		TradeDate:        tradeDate,
	}
	if cfg.TopLayer2 < rotation.DefaultConfig().RotationExitRank {
		cfg.TopLayer2 = rotation.DefaultConfig().RotationExitRank
	}
	res, err := daily.Run(ctx, st, cfg)
	if err != nil {
		log.Printf("[Scheduler] ❌ 选股失败: %v", err)
		return false
	}
	log.Printf("[Scheduler] ✅ 选股完成：%d 只→写入 %d 只，耗时 %dms",
		res.Total, res.Layer2, res.ElapsedMs)
	sc.ForceRefresh()
	log.Println("[Scheduler] 候选池已刷新，新排名将在下一个 Tick 生效")

	// 选股刷新后立即预热新候选股的历史数据，确保 Return5d/EMA20/Volatility 可用。
	if ep, ok := dp.(*eastmoney.Provider); ok {
		candidates := sc.Screen()
		go func(syms []string) {
			pCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			closesMap := eastmoney.FetchDailyCloses(pCtx, syms, 21)
			for sym, points := range closesMap {
				ep.PreWarm(sym, points)
			}
			log.Printf("[PreWarm] ✅ 选股刷新后预热完成：%d/%d 只", len(closesMap), len(syms))
		}(candidates)
	}
	return true
}


// nextAlphaRunTimeAfter returns the first future scheduled time that falls on a trading day.
func nextAlphaRunTimeAfter(now time.Time, hour, min int, loc *time.Location, cal core.TradingCalendar) time.Time {
	now = now.In(loc)
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	for cal != nil && !cal.IsTradeDay(candidate) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

// nextDailyTime returns the next occurrence of hour:min on a trading day in the given timezone.
func nextDailyTime(hour, min int, loc *time.Location) time.Time {
	return nextAlphaRunTimeAfter(time.Now().In(loc), hour, min, loc, calendar.New())
}
