// Package deviation accumulates ExecutionRecords and produces a statistical
// slippage and execution quality report.
//
// Metrics computed:
//   - Fill rate (total / filled / partial / rejected)
//   - Slippage distribution (mean, median, max, std-dev) for BUY and SELL
//   - Execution latency (mean, max) in milliseconds
//   - Directional slippage by side
package deviation

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"astock_trade/core"
)

// Report is the output of Analyzer.Report().
type Report struct {
	TotalOrders    int
	FilledOrders   int
	PartialOrders  int
	RejectedOrders int
	FillRate       float64 // (filled+partial) / total × 100 (%)

	// Slippage statistics across all non-rejected executions.
	AvgSlippagePct    float64 // mean slippage (%)
	MedianSlippagePct float64 // median slippage (%)
	MaxSlippagePct    float64 // worst (most positive) slippage (%)
	StdSlippagePct    float64 // standard deviation of slippage (%)

	// Slippage by direction.
	BuyAvgSlippagePct  float64 // mean slippage for BUY orders
	SellAvgSlippagePct float64 // mean slippage for SELL orders

	// Latency in milliseconds.
	AvgLatencyMs int64 // mean (ExecutionTime − SignalTime)
	MaxLatencyMs int64 // worst single-order latency
}

// Analyzer accumulates ExecutionRecords and computes deviation statistics.
type Analyzer struct {
	records []core.ExecutionRecord
}

// New returns an empty Analyzer.
func New() *Analyzer {
	return &Analyzer{}
}

// Add appends a single record.
func (a *Analyzer) Add(rec core.ExecutionRecord) {
	a.records = append(a.records, rec)
}

// AddAll appends all records from a broker.Records() call.
func (a *Analyzer) AddAll(recs []core.ExecutionRecord) {
	a.records = append(a.records, recs...)
}

// Report computes and returns the deviation report.
// Returns a zero-value Report if no records have been added.
func (a *Analyzer) Report() Report {
	if len(a.records) == 0 {
		return Report{}
	}

	r := Report{TotalOrders: len(a.records)}

	var slippages []float64
	var totalSlip float64
	var buySlip, buyCount float64
	var sellSlip, sellCount float64
	var totalLatency int64

	for _, rec := range a.records {
		switch rec.Status {
		case "FILLED":
			r.FilledOrders++
		case "PARTIAL":
			r.PartialOrders++
		case "REJECTED":
			r.RejectedOrders++
		}

		if rec.Status == "REJECTED" {
			continue
		}

		slippages = append(slippages, rec.SlippagePct)
		totalSlip += rec.SlippagePct

		switch rec.Side {
		case "BUY":
			buySlip += rec.SlippagePct
			buyCount++
		case "SELL":
			sellSlip += rec.SlippagePct
			sellCount++
		}

		totalLatency += rec.Latency
		if rec.Latency > r.MaxLatencyMs {
			r.MaxLatencyMs = rec.Latency
		}
	}

	executed := r.FilledOrders + r.PartialOrders
	if r.TotalOrders > 0 {
		r.FillRate = float64(executed) / float64(r.TotalOrders) * 100
	}

	n := len(slippages)
	if n > 0 {
		r.AvgSlippagePct = totalSlip / float64(n)
		r.AvgLatencyMs = totalLatency / int64(n)

		sorted := make([]float64, n)
		copy(sorted, slippages)
		sort.Float64s(sorted)

		if n%2 == 0 {
			r.MedianSlippagePct = (sorted[n/2-1] + sorted[n/2]) / 2
		} else {
			r.MedianSlippagePct = sorted[n/2]
		}
		r.MaxSlippagePct = sorted[n-1]

		var variance float64
		for _, s := range slippages {
			d := s - r.AvgSlippagePct
			variance += d * d
		}
		r.StdSlippagePct = math.Sqrt(variance / float64(n))
	}

	if buyCount > 0 {
		r.BuyAvgSlippagePct = buySlip / buyCount
	}
	if sellCount > 0 {
		r.SellAvgSlippagePct = sellSlip / sellCount
	}

	return r
}

// PrintReport prints a formatted deviation analysis to stdout.
func (a *Analyzer) PrintReport() {
	r := a.Report()

	var b strings.Builder
	line := strings.Repeat("═", 52)
	sep := strings.Repeat("─", 52)

	b.WriteString(line + "\n")
	b.WriteString("  实盘偏差分析报告  (Execution Deviation Report)\n")
	b.WriteString(line + "\n")
	b.WriteString(fmt.Sprintf("  总订单数       %d\n", r.TotalOrders))
	b.WriteString(fmt.Sprintf("    ├ 全额成交    %d\n", r.FilledOrders))
	b.WriteString(fmt.Sprintf("    ├ 部分成交    %d\n", r.PartialOrders))
	b.WriteString(fmt.Sprintf("    └ 拒绝成交    %d  (含高波动拒单)\n", r.RejectedOrders))
	b.WriteString(fmt.Sprintf("  成交率         %.1f%%\n", r.FillRate))
	b.WriteString(sep + "\n")
	b.WriteString("  滑点统计 (Slippage Statistics)\n")
	b.WriteString(fmt.Sprintf("    均值          %+.4f%%\n", r.AvgSlippagePct))
	b.WriteString(fmt.Sprintf("    中位数        %+.4f%%\n", r.MedianSlippagePct))
	b.WriteString(fmt.Sprintf("    最大滑点      %+.4f%%\n", r.MaxSlippagePct))
	b.WriteString(fmt.Sprintf("    标准差        %.4f%%\n", r.StdSlippagePct))
	b.WriteString(fmt.Sprintf("    买入均滑点    %+.4f%%  (正值=成本增加)\n", r.BuyAvgSlippagePct))
	b.WriteString(fmt.Sprintf("    卖出均滑点    %+.4f%%  (负值=收益减少)\n", r.SellAvgSlippagePct))
	b.WriteString(sep + "\n")
	b.WriteString("  延迟分析 (Latency Analysis)\n")
	b.WriteString(fmt.Sprintf("    平均延迟      %d ms\n", r.AvgLatencyMs))
	b.WriteString(fmt.Sprintf("    最大延迟      %d ms\n", r.MaxLatencyMs))
	b.WriteString(line)

	fmt.Println(b.String())
}
