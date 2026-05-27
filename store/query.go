package store

import (
	"context"
	"fmt"
	"time"
)

// ─── Response types (JSON-serializable) ──────────────────────────────────────

type EquityQueryRow struct {
	Timestamp     int64   `json:"ts"`
	Equity        float64 `json:"equity"`
	Drawdown      float64 `json:"drawdown"`
	Cash          float64 `json:"cash"`
	PositionValue float64 `json:"position_value"`
}

type ExecQueryRow struct {
	ID               int64   `json:"id"`
	OrderID          string  `json:"order_id"`
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"`
	Qty              int     `json:"qty"`
	Price            float64 `json:"price"`
	TheoreticalPrice float64 `json:"theoretical_price"`
	Slippage         float64 `json:"slippage"`
	Status           string  `json:"status"`
	SignalTime       int64   `json:"signal_time"`
	ExecutionTime    int64   `json:"execution_time"`
	LatencyMs        int64   `json:"latency_ms"`
	StrategyName     string  `json:"strategy_name"`
}

type PosQueryRow struct {
	Symbol        string  `json:"symbol"`
	Qty           int     `json:"qty"`
	AvgPrice      float64 `json:"avg_price"`
	MarketValue   float64 `json:"market_value"`
	UnrealizedPnl float64 `json:"unrealized_pnl"`
	UpdatedAt     int64   `json:"updated_at"`
}

type RiskQueryRow struct {
	ID          int64   `json:"id"`
	Timestamp   int64   `json:"ts"`
	EventType   string  `json:"event_type"`
	Drawdown    float64 `json:"drawdown"`
	PositionPct float64 `json:"position_pct"`
	Description string  `json:"description"`
}

type StatusQueryRow struct {
	Timestamp          int64   `json:"ts"`
	Streak             int     `json:"streak"`
	RiskLevel          string  `json:"risk_level"`
	MaxPositionPct     float64 `json:"max_position_pct"`
	IsOpeningAllowed   bool    `json:"is_opening_allowed"`
	IsKillSwitchActive bool    `json:"is_kill_switch_active"`
	AnomalyCount       int     `json:"anomaly_count"`
}

// ─── Query helpers ────────────────────────────────────────────────────────────

func sinceMs(rangeStr string) int64 {
	now := time.Now().UnixMilli()
	switch rangeStr {
	case "1d":
		return now - 24*3600*1000
	case "7d":
		return now - 7*24*3600*1000
	default:
		return 0
	}
}

// ─── Query methods ────────────────────────────────────────────────────────────

// QueryEquityCurve returns equity-curve rows after the given range.
// rangeStr: "1d" | "7d" | "all"
func (s *Store) QueryEquityCurve(ctx context.Context, rangeStr string) ([]EquityQueryRow, error) {
	since := sinceMs(rangeStr)
	rows, err := s.pool.Query(ctx, `
		SELECT timestamp, equity, drawdown, cash, position_value
		FROM equity_curve
		WHERE timestamp >= $1
		ORDER BY timestamp ASC`, since)
	if err != nil {
		return nil, fmt.Errorf("QueryEquityCurve: %w", err)
	}
	defer rows.Close()

	var out []EquityQueryRow
	for rows.Next() {
		var r EquityQueryRow
		if err := rows.Scan(&r.Timestamp, &r.Equity, &r.Drawdown, &r.Cash, &r.PositionValue); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryExecutions returns up to limit recent executions, optionally filtered by symbol.
// limit defaults to 100 when 0.
func (s *Store) QueryExecutions(ctx context.Context, symbol string, limit int) ([]ExecQueryRow, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		rows interface{ Next() bool; Scan(...any) error; Err() error; Close() }
		err  error
	)
	if symbol != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id,order_id,symbol,side,qty,price,theoretical_price,slippage,
			       status,signal_time,execution_time,latency_ms,strategy_name
			FROM executions
			WHERE symbol=$1
			ORDER BY execution_time DESC
			LIMIT $2`, symbol, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id,order_id,symbol,side,qty,price,theoretical_price,slippage,
			       status,signal_time,execution_time,latency_ms,strategy_name
			FROM executions
			ORDER BY execution_time DESC
			LIMIT $1`, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("QueryExecutions: %w", err)
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

// QueryPositions returns all current positions.
func (s *Store) QueryPositions(ctx context.Context) ([]PosQueryRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT symbol,qty,avg_price,market_value,unrealized_pnl,updated_at FROM positions`)
	if err != nil {
		return nil, fmt.Errorf("QueryPositions: %w", err)
	}
	defer rows.Close()

	var out []PosQueryRow
	for rows.Next() {
		var r PosQueryRow
		if err := rows.Scan(&r.Symbol, &r.Qty, &r.AvgPrice,
			&r.MarketValue, &r.UnrealizedPnl, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryRiskEvents returns up to limit recent risk events.
func (s *Store) QueryRiskEvents(ctx context.Context, limit int) ([]RiskQueryRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,timestamp,event_type,drawdown,position_pct,description
		FROM risk_events
		ORDER BY timestamp DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("QueryRiskEvents: %w", err)
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

// QueryLatestSystemStatus returns the most recent system-status row, or nil if none.
func (s *Store) QueryLatestSystemStatus(ctx context.Context) (*StatusQueryRow, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT timestamp,streak,risk_level,max_position_pct,
		       is_opening_allowed,is_kill_switch_active,anomaly_count
		FROM system_status
		ORDER BY timestamp DESC
		LIMIT 1`)
	var r StatusQueryRow
	err := row.Scan(&r.Timestamp, &r.Streak, &r.RiskLevel, &r.MaxPositionPct,
		&r.IsOpeningAllowed, &r.IsKillSwitchActive, &r.AnomalyCount)
	if err != nil {
		// pgx uses pgx.ErrNoRows, but we check string to avoid import
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("QueryLatestSystemStatus: %w", err)
	}
	return &r, nil
}
