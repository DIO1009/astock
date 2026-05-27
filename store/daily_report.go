package store

import (
	"context"
	"fmt"
	"time"
)

// ── daily_reports table ────────────────────────────────────────────────────────

const (
	ReportStatusPending = "PENDING"
	ReportStatusSuccess = "SUCCESS"
	ReportStatusFailed  = "FAILED"
)

// DailyReportRow mirrors the daily_reports table.
type DailyReportRow struct {
	Date        time.Time
	Status      string
	ReportPath  string
	GeneratedAt time.Time
	ErrorMsg    string
	RetryCount  int
}

// UpsertDailyReport inserts or updates the daily report record for the given date.
func (s *Store) UpsertDailyReport(ctx context.Context, r DailyReportRow) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO daily_reports (date, status, report_path, generated_at, error_msg, retry_count)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (date) DO UPDATE SET
			status       = EXCLUDED.status,
			report_path  = EXCLUDED.report_path,
			generated_at = EXCLUDED.generated_at,
			error_msg    = EXCLUDED.error_msg,
			retry_count  = EXCLUDED.retry_count
	`, r.Date, r.Status, r.ReportPath, r.GeneratedAt, r.ErrorMsg, r.RetryCount)
	return err
}

// GetDailyReport returns the report row for the given date, or nil if not found.
func (s *Store) GetDailyReport(ctx context.Context, date time.Time) (*DailyReportRow, error) {
	d := date.Truncate(24 * time.Hour)
	row := s.pool.QueryRow(ctx, `
		SELECT date, status, report_path, generated_at, error_msg, retry_count
		FROM daily_reports WHERE date = $1`, d)

	var r DailyReportRow
	err := row.Scan(&r.Date, &r.Status, &r.ReportPath, &r.GeneratedAt, &r.ErrorMsg, &r.RetryCount)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("GetDailyReport: %w", err)
	}
	return &r, nil
}

// ── Daily data query helpers ───────────────────────────────────────────────────

// QueryDayExecutions returns all executions whose execution_time falls on the
// given calendar date (UTC+8 CST).
func (s *Store) QueryDayExecutions(ctx context.Context, date time.Time) ([]ExecQueryRow, error) {
	cst := time.FixedZone("CST", 8*3600)
	d := date.In(cst).Truncate(24 * time.Hour)
	startMs := d.UnixMilli()
	endMs := d.Add(24 * time.Hour).UnixMilli()

	rows, err := s.pool.Query(ctx, `
		SELECT id,order_id,symbol,side,qty,price,theoretical_price,slippage,
		       status,signal_time,execution_time,latency_ms,strategy_name
		FROM executions
		WHERE execution_time >= $1 AND execution_time < $2
		ORDER BY execution_time ASC`, startMs, endMs)
	if err != nil {
		return nil, fmt.Errorf("QueryDayExecutions: %w", err)
	}
	defer rows.Close()

	var out []ExecQueryRow
	for rows.Next() {
		var r ExecQueryRow
		if err := rows.Scan(&r.ID, &r.OrderID, &r.Symbol, &r.Side, &r.Qty,
			&r.Price, &r.TheoreticalPrice, &r.Slippage, &r.Status,
			&r.SignalTime, &r.ExecutionTime, &r.LatencyMs, &r.StrategyName,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryDayRiskEvents returns risk events for the given calendar date.
func (s *Store) QueryDayRiskEvents(ctx context.Context, date time.Time) ([]RiskQueryRow, error) {
	cst := time.FixedZone("CST", 8*3600)
	d := date.In(cst).Truncate(24 * time.Hour)
	startMs := d.UnixMilli()
	endMs := d.Add(24 * time.Hour).UnixMilli()

	rows, err := s.pool.Query(ctx, `
		SELECT id,timestamp,event_type,drawdown,position_pct,description
		FROM risk_events
		WHERE timestamp >= $1 AND timestamp < $2
		ORDER BY timestamp ASC`, startMs, endMs)
	if err != nil {
		return nil, fmt.Errorf("QueryDayRiskEvents: %w", err)
	}
	defer rows.Close()

	var out []RiskQueryRow
	for rows.Next() {
		var r RiskQueryRow
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.EventType,
			&r.Drawdown, &r.PositionPct, &r.Description); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryDayEquity returns the first and last equity_curve rows for the given date.
// open and close will be zero-value if no data is available for that date.
func (s *Store) QueryDayEquity(ctx context.Context, date time.Time) (open, close EquityQueryRow, err error) {
	cst := time.FixedZone("CST", 8*3600)
	d := date.In(cst).Truncate(24 * time.Hour)
	startMs := d.UnixMilli()
	endMs := d.Add(24 * time.Hour).UnixMilli()

	// opening equity (earliest row of the day)
	err = s.pool.QueryRow(ctx, `
		SELECT timestamp,equity,drawdown,cash,position_value
		FROM equity_curve
		WHERE timestamp >= $1 AND timestamp < $2
		ORDER BY timestamp ASC LIMIT 1`, startMs, endMs).
		Scan(&open.Timestamp, &open.Equity, &open.Drawdown, &open.Cash, &open.PositionValue)
	if err != nil && err.Error() == "no rows in result set" {
		err = nil // no data for today — that's OK
		return
	}

	// closing equity (latest row of the day)
	err = s.pool.QueryRow(ctx, `
		SELECT timestamp,equity,drawdown,cash,position_value
		FROM equity_curve
		WHERE timestamp >= $1 AND timestamp < $2
		ORDER BY timestamp DESC LIMIT 1`, startMs, endMs).
		Scan(&close.Timestamp, &close.Equity, &close.Drawdown, &close.Cash, &close.PositionValue)
	if err != nil && err.Error() == "no rows in result set" {
		err = nil
	}
	return
}

// QueryTopAlphaRankings returns the top-n alpha rankings for the given date.
// Falls back to the latest available date if the exact date has no data.
func (s *Store) QueryTopAlphaRankings(ctx context.Context, date time.Time, n int) ([]AlphaRankRow, error) {
	d := date.Truncate(24 * time.Hour)
	rows, err := s.pool.Query(ctx, `
		SELECT date,symbol,name,score,rank,ret5d,ret20d,turnover,volume_ratio,mkt_cap,price
		FROM alpha_rankings
		WHERE date = (
			SELECT COALESCE(
				(SELECT MAX(date) FROM alpha_rankings WHERE date <= $1),
				(SELECT MAX(date) FROM alpha_rankings)
			)
		)
		ORDER BY rank ASC
		LIMIT $2`, d, n)
	if err != nil {
		return nil, fmt.Errorf("QueryTopAlphaRankings: %w", err)
	}
	defer rows.Close()

	var out []AlphaRankRow
	for rows.Next() {
		var r AlphaRankRow
		if err := rows.Scan(&r.Date, &r.Symbol, &r.Name, &r.Score, &r.Rank,
			&r.Ret5d, &r.Ret20d, &r.Turnover, &r.VolumeRatio, &r.MktCap, &r.Price); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
