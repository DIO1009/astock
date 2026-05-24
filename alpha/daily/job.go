package daily

import (
	"context"
	"fmt"
	"log"
	"time"

	"astock_trade/alpha/universe"
	"astock_trade/store"
)

var cst = time.FixedZone("CST", 8*3600)

type Config struct {
	TopLayer1       int
	TopLayer2       int
	ScanTimeoutSecs int
	ExcludedPrefixes []string
	RequireVolume   bool
	TradeDate       time.Time
}

func DefaultConfig() Config {
	return Config{
		TopLayer1:       200,
		TopLayer2:       50,
		ScanTimeoutSecs: 300,
	}
}

type Result struct {
	Date      time.Time
	Total     int
	Layer1    int
	Layer2    int
	ElapsedMs int64
}

func resolveRunDate(now time.Time, cfg Config) time.Time {
	runDate := now
	if !cfg.TradeDate.IsZero() {
		runDate = cfg.TradeDate
	}
	runDate = runDate.In(cst)
	return time.Date(runDate.Year(), runDate.Month(), runDate.Day(), 0, 0, 0, 0, cst)
}

func Run(ctx context.Context, st *store.Store, cfg Config) (Result, error) {
	t0 := time.Now()
	res := Result{Date: resolveRunDate(t0, cfg)}

	timeout := time.Duration(cfg.ScanTimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(DefaultConfig().ScanTimeoutSecs) * time.Second
	}

	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stocks, err := universe.NewFetcher().FetchAll(fetchCtx)
	if err != nil {
		return res, err
	}
	res.Total = len(stocks)
	if res.Total < 100 {
		return res, fmt.Errorf("alpha daily universe too small: %d", res.Total)
	}

	topLayer1 := cfg.TopLayer1
	if topLayer1 <= 0 {
		topLayer1 = DefaultConfig().TopLayer1
	}
	topLayer2 := cfg.TopLayer2
	if topLayer2 <= 0 {
		topLayer2 = DefaultConfig().TopLayer2
	}

	scored := universe.ScoreAll(stocks, cfg.ExcludedPrefixes, cfg.RequireVolume)
	if len(scored) > topLayer1 {
		scored = scored[:topLayer1]
	}
	res.Layer1 = len(scored)

	layer2 := universe.FilterLayer2(scored, topLayer2)
	res.Layer2 = len(layer2)

	rows := make([]store.AlphaRankRow, 0, len(layer2))
	for i, stock := range layer2 {
		rows = append(rows, store.AlphaRankRow{
			Date:        res.Date,
			Rank:        i + 1,
			Symbol:      stock.Symbol,
			Name:        stock.Name,
			Market:      stock.Market,
			Price:       stock.Price,
			ChangeP:     stock.ChangeP,
			Volume:      stock.Volume,
			Amount:      stock.Amount,
			Turnover:    stock.Turnover,
			PE:          stock.PE,
			VolumeRatio: stock.VolumeRatio,
			MktCap:      stock.MktCap,
			FloatCap:    stock.FloatCap,
			PB:          stock.PB,
			Ret5d:       stock.Ret5d,
			Ret10d:      stock.Ret10d,
			Ret20d:      stock.Ret20d,
		})
	}
	if err := st.UpsertAlphaRanks(ctx, rows); err != nil {
		return res, err
	}

	res.ElapsedMs = time.Since(t0).Milliseconds()
	log.Printf("alpha daily completed date=%s total=%d layer1=%d layer2=%d elapsed_ms=%d", res.Date.Format("2006-01-02"), res.Total, res.Layer1, res.Layer2, res.ElapsedMs)
	return res, nil
}
