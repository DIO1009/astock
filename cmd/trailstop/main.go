// Command trailstop analyses TRAIL_STOP exits to find "遗珠" (missed profit).
//
// It queries PostgreSQL for all SELL executions with reason=TRAIL_STOP,
// fetches post-exit daily K-line data, and reports the highest price
// within 5/10/20 trading-day windows after each exit.
//
// Usage:
//
//	go run ./cmd/trailstop/ --dsn "postgres://user:pass@host:5432/db"
//	PG_DSN="postgres://..." go run ./cmd/trailstop/
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"astock_trade/analysis/trailstop"
	"astock_trade/store"
)

func main() {
	dsn := flag.String("dsn", "", "PostgreSQL DSN (default: $PG_DSN)")
	flag.Parse()

	if *dsn == "" {
		*dsn = os.Getenv("PG_DSN")
	}
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "trailstop: --dsn not set and PG_DSN env is empty")
		os.Exit(1)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, store.Config{DSN: *dsn})
	if err != nil {
		fmt.Fprintf(os.Stderr, "trailstop: connect DB: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	analyzer := trailstop.New(s)
	results, err := analyzer.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "trailstop: %v\n", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Println("No TRAIL_STOP exit records found.")
		return
	}

	printReport(results)
}

func printReport(results []trailstop.PeakResult) {
	fmt.Println(strings.Repeat("=", 110))
	fmt.Printf("%-10s %-12s %10s %10s %10s %10s %10s %10s %10s %10s\n",
		"Symbol", "ExitDate", "EntryPrice", "ExitPrice", "Peak5d", "Peak10d", "Peak20d", "Gain5d%", "Gain10d%", "Gain20d%")
	fmt.Println(strings.Repeat("-", 110))

	for _, r := range results {
		if r.Skipped {
			fmt.Printf("%-10s %-12s SKIP: %s\n", r.Symbol, r.ExitDate, r.SkipReason)
			continue
		}
		fmt.Printf("%-10s %-12s %10.2f %10.2f %10.2f %10.2f %10.2f %9.1f%% %9.1f%% %9.1f%%\n",
			r.Symbol, r.ExitDate,
			r.EntryPrice, r.ExitPrice,
			r.Peak5d, r.Peak10d, r.Peak20d,
			r.Gain5d*100, r.Gain10d*100, r.Gain20d*100)
	}

	fmt.Println(strings.Repeat("=", 110))

	// Summary statistics (only non-skipped results).
	var count int
	var sumG5, sumG10, sumG20 float64
	var maxG5, maxG10, maxG20 float64
	for _, r := range results {
		if r.Skipped {
			continue
		}
		count++
		sumG5 += r.Gain5d
		sumG10 += r.Gain10d
		sumG20 += r.Gain20d
		if r.Gain5d > maxG5 {
			maxG5 = r.Gain5d
		}
		if r.Gain10d > maxG10 {
			maxG10 = r.Gain10d
		}
		if r.Gain20d > maxG20 {
			maxG20 = r.Gain20d
		}
	}

	if count > 0 {
		fmt.Printf("\nSummary (%d exits analysed):\n", count)
		fmt.Printf("  Avg missed gain: 5d=%5.1f%%  10d=%5.1f%%  20d=%5.1f%%\n",
			sumG5/float64(count)*100, sumG10/float64(count)*100, sumG20/float64(count)*100)
		fmt.Printf("  Max missed gain: 5d=%5.1f%%  10d=%5.1f%%  20d=%5.1f%%\n",
			maxG5*100, maxG10*100, maxG20*100)
	}
}
