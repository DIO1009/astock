package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"astock_trade/core"
	"astock_trade/monitor"
	"astock_trade/performance"
	"astock_trade/safety"
	"astock_trade/store"
)

type restoredRuntimeState struct {
	Cash             float64
	ExecutionRecords []core.ExecutionRecord
	ClosedTrades     []core.ClosedTrade
	EquityCurve      []float64
}


type symbolPositionState struct {
	Qty      int
	AvgPrice float64
}

var (
	reasonAvgPattern = regexp.MustCompile(`avg=\s*([0-9]+(?:\.[0-9]+)?)`)
	reasonPnlPattern = regexp.MustCompile(`pnl=\s*([+-]?[0-9]+(?:\.[0-9]+)?)%`)
)

func restoreRuntimeState(execPath string, initialCash float64, lookback time.Duration) (*restoredRuntimeState, error) {
	state := &restoredRuntimeState{Cash: initialCash}

	f, err := os.Open(execPath)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open execution log: %w", err)
	}
	defer f.Close()

	var allRecords []core.ExecutionRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var rec core.ExecutionRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("decode execution log: %w", err)
		}
		allRecords = append(allRecords, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan execution log: %w", err)
	}

	if len(allRecords) == 0 {
		return state, nil
	}

	cutoffTs := int64(0)
	if lookback > 0 {
		latestTs := allRecords[len(allRecords)-1].ExecutionTime
		cutoffTs = latestTs - lookback.Milliseconds()
	}

	openPositions := make(map[string]symbolPositionState)
	for _, rec := range allRecords {
		if rec.ExecutionTime < cutoffTs {
			continue
		}
		state.ExecutionRecords = append(state.ExecutionRecords, rec)

		if rec.FilledQty <= 0 || (rec.Status != "FILLED" && rec.Status != "PARTIAL") {
			continue
		}

		qty := rec.FilledQty
		price := rec.ActualPrice
		if price <= 0 {
			price = rec.TheoreticalPrice
		}
		if qty <= 0 || price <= 0 {
			continue
		}

		switch rec.Side {
		case "BUY":
			state.Cash -= price * float64(qty)
			pos := openPositions[rec.Symbol]
			totalQty := pos.Qty + qty
			if totalQty > 0 {
				pos.AvgPrice = (pos.AvgPrice*float64(pos.Qty) + price*float64(qty)) / float64(totalQty)
			} else {
				pos.AvgPrice = price
			}
			pos.Qty = totalQty
			openPositions[rec.Symbol] = pos
		case "SELL":
			state.Cash += price * float64(qty)
			pos := openPositions[rec.Symbol]
			entryPrice := pos.AvgPrice
			if parsedAvg, ok := parseReasonFloat(rec.Reason, reasonAvgPattern); ok && parsedAvg > 0 {
				entryPrice = parsedAvg
			}
			pnlPct := 0.0
			if parsedPnl, ok := parseReasonFloat(rec.Reason, reasonPnlPattern); ok {
				pnlPct = parsedPnl
			} else if entryPrice > 0 {
				pnlPct = (price - entryPrice) / entryPrice * 100
			}
			state.ClosedTrades = append(state.ClosedTrades, core.ClosedTrade{
				Symbol:     rec.Symbol,
				EntryPrice: entryPrice,
				ExitPrice:  price,
				Quantity:   qty,
				PnlPct:     pnlPct,
				HoldTicks:  0,
				ExitReason: parseExitReason(rec.Reason),
				Timestamp:  rec.ExecutionTime,
			})
			pos.Qty -= qty
			if pos.Qty <= 0 {
				delete(openPositions, rec.Symbol)
			} else {
				openPositions[rec.Symbol] = pos
			}
		}
	}

	return state, nil
}

func restoreSafetyState(ctx context.Context, st *store.Store, guard *safety.Guard) {
	if st == nil || guard == nil {
		return
	}
	restoreCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	row, err := st.QueryLatestSystemStatus(restoreCtx)
	if err != nil || row == nil {
		if err != nil {
			log.Printf("[Paper] 恢复 SafetyGuard 状态失败: %v", err)
		}
		return
	}
	guard.Restore(row.Streak, row.AnomalyCount, row.IsOpeningAllowed, row.IsKillSwitchActive)
}

func hydrateRuntimeFromDB(ctx context.Context, st *store.Store, perfTracker *performance.Tracker, mon *monitor.Monitor, positions []core.Position) {
	if st == nil {
		return
	}
	restoreCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	rows, err := st.QueryEquityCurve(restoreCtx, "all")
	if err != nil || len(rows) == 0 {
		if err != nil {
			log.Printf("[Paper] 恢复权益曲线失败: %v", err)
		}
		return
	}
	rows = sanitizeEquityRows(rows)
	if len(rows) == 0 {
		return
	}

	if restoreLookback > 0 {
		latestTs := rows[len(rows)-1].Timestamp
		cutoffTs := latestTs - restoreLookback.Milliseconds()
		filtered := rows[:0]
		for _, row := range rows {
			if row.Timestamp >= cutoffTs {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
		if len(rows) == 0 {
			return
		}
	}

	curve := make([]float64, 0, len(rows))
	peak := 0.0
	for _, row := range rows {
		curve = append(curve, row.Equity)
		if row.Equity > peak {
			peak = row.Equity
		}
	}
	cash := rows[len(rows)-1].Cash
	perfTracker.Restore(cash, curve, perfTracker.ClosedTrades())

	last := rows[len(rows)-1]
	initialCapital := perfTracker.Report().InitialCapital
	drawdown := 0.0
	if initialCapital > 0 && last.Equity < initialCapital {
		drawdown = (initialCapital - last.Equity) / initialCapital * 100
	}
	level := core.RiskNormal
	switch {
	case drawdown >= drawdownEmergencyPct:
		level = core.RiskEmergency
	case drawdown >= drawdownDefensePct:
		level = core.RiskDefense
	case drawdown >= drawdownCautionPct:
		level = core.RiskCaution
	}
	mon.Restore(core.MonitorState{
		Timestamp:   last.Timestamp,
		Equity:      last.Equity,
		PeakEquity:  peak,
		DrawdownPct: drawdown,
		RiskLevel:   level,
		Positions:   append([]core.Position(nil), positions...),
		TradeCount:  perfTracker.Report().TradeCount,
		WinRate:     perfTracker.Report().WinRate,
	})
}

func sanitizeEquityRows(rows []store.EquityQueryRow) []store.EquityQueryRow {
	if len(rows) < 3 {
		return append([]store.EquityQueryRow(nil), rows...)
	}

	out := make([]store.EquityQueryRow, 0, len(rows))
	for i, row := range rows {
		if i > 0 && i < len(rows)-1 {
			prev := rows[i-1]
			next := rows[i+1]
			if isIsolatedEquitySpike(prev.Equity, row.Equity, next.Equity) {
				log.Printf("[Paper] 忽略异常权益点 ts=%d equity=%.2f prev=%.2f next=%.2f",
					row.Timestamp, row.Equity, prev.Equity, next.Equity)
				continue
			}
		}
		out = append(out, row)
	}
	return out
}

func isIsolatedEquitySpike(prev, cur, next float64) bool {
	if prev <= 0 || cur <= 0 || next <= 0 {
		return false
	}
	jumpFromPrev := math.Abs(cur-prev) / prev
	revertToNext := math.Abs(cur-next) / cur
	baselineMove := math.Abs(next-prev) / prev
	return jumpFromPrev >= 0.20 &&
		revertToNext >= 0.20 &&
		baselineMove <= 0.05
}

func parseExitReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "SELL"
	}
	fields := strings.Fields(reason)
	if len(fields) == 0 {
		return "SELL"
	}
	return fields[0]
}

func parseReasonFloat(reason string, pattern *regexp.Regexp) (float64, bool) {
	matches := pattern.FindStringSubmatch(reason)
	if len(matches) < 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
