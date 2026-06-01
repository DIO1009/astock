package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"time"
)
func resolveDataPath(fallbackPath string, allSyms []string) string {
	// Priority 1: explicit env var
	if envPath := os.Getenv("ASTOCK_DATA_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			log.Printf("[Data] ✅ 使用指定数据文件 (ASTOCK_DATA_PATH): %s", envPath)
			return envPath
		}
		log.Printf("[Data] ⚠️  ASTOCK_DATA_PATH 指定的文件不存在: %s，降级至合成数据", envPath)
	}

	// Priority 2: real_market_data.csv in working directory
	realPath := "real_market_data.csv"
	if info, err := os.Stat(realPath); err == nil && info.Size() > 100 {
		log.Printf("[Data] ✅ 使用真实行情数据: %s (%.1fKB)", realPath, float64(info.Size())/1024)
		log.Printf("[Data]    来源: python3 scripts/fetch_data.py")
		return realPath
	}

	// Priority 3: generate synthetic data (fallback)
	log.Printf("[Data] ⚠️  未找到真实数据文件，生成合成数据（价格不反映真实市场）")
	log.Printf("[Data]    建议: pip3 install akshare && python3 scripts/fetch_data.py")
	generateSyntheticCSV(fallbackPath, allSyms, 120)
	return fallbackPath
}

// generateSyntheticCSV creates a CSV file with synthetic OHLCV data for
// the given symbols over nDays trading days, starting from current market-level
// seed prices. Used ONLY as a fallback when real market data is unavailable.
//
// IMPORTANT: This is NOT real market data. Prices are random-walk simulations
// starting from approximate current market prices (updated 2025-Q1).
// Use scripts/fetch_data.py to download real data.
func generateSyntheticCSV(path string, symbols []string, nDays int) {
	rng := rand.New(rand.NewSource(42)) // fixed seed for reproducibility

	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("[Paper] 无法创建数据文件: %v", err)
	}
	defer f.Close()

	fmt.Fprintln(f, "date,symbol,open,high,low,close,volume")

	// 以当前日期回推 nDays 个自然日作为合成数据起点
	startDate := time.Now().AddDate(0, 0, -int(float64(nDays)*1.7))
	startDate = time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, time.Local)

	// 种子价格对应 2025-Q1 实际市价水平（不复权）
	// 每次系统更新时应同步校准此处价格，避免合成数据与真实市价偏差过大
	seedPrices := map[string]float64{
		"600519": 1420.0, // 贵州茅台 (实际约 1420)
		"000858": 88.0,   // 五粮液   (实际约 88)
		"300750": 82.0,   // 宁德时代 (实际约 82)
		"000300": 3750.0, // 沪深300  (实际约 3750)
	}

	prices := make(map[string]float64)
	for sym, p := range seedPrices {
		prices[sym] = p
	}

	day := 0
	for day < nDays {
		// Skip weekends
		if startDate.Weekday() == time.Saturday || startDate.Weekday() == time.Sunday {
			startDate = startDate.AddDate(0, 0, 1)
			continue
		}
		dateStr := startDate.Format("2006-01-02")

		for _, sym := range symbols {
			prev := prices[sym]
			// Random walk with slight upward drift
			drift := rng.NormFloat64()*0.015 + 0.0005
			closeP := math.Max(1.0, prev*(1+drift))
			openP := prev * (1 + rng.NormFloat64()*0.005)
			highP := math.Max(openP, closeP) * (1 + rng.Float64()*0.01)
			lowP := math.Min(openP, closeP) * (1 - rng.Float64()*0.01)
			vol := int64(rng.Intn(5_000_000) + 1_000_000)

			fmt.Fprintf(f, "%s,%s,%.4f,%.4f,%.4f,%.4f,%d\n",
				dateStr, sym, openP, highP, lowP, closeP, vol)
			prices[sym] = closeP
		}

		startDate = startDate.AddDate(0, 0, 1)
		day++
	}
	log.Printf("[Paper] 已生成 %s (%d 交易日, %d 个标的)", path, nDays, len(symbols))
}