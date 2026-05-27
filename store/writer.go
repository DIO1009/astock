package store

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"astock_trade/core"
)

// ─── Row types ────────────────────────────────────────────────────────────────

// EquityRow is one point on the equity curve.
type EquityRow struct {
	Timestamp     int64
	Equity        float64
	Drawdown      float64
	Cash          float64
	PositionValue float64
}

// PosRow is one position snapshot.
type PosRow struct {
	Symbol        string
	Qty           int
	AvgPrice      float64
	MarketValue   float64
	UnrealizedPnl float64
	UpdatedAt     int64
}

// RiskRow is one risk event.
type RiskRow struct {
	Timestamp   int64
	EventType   string
	Drawdown    float64
	PositionPct float64
	Description string
}

// StatusRow is one system-status snapshot.
type StatusRow struct {
	Timestamp        int64
	Streak           int
	RiskLevel        string
	MaxPositionPct   float64
	AllowOpen        bool
	KillSwitchActive bool
	AnomalyCount     int
}

// execItem wraps an ExecutionRecord with an extracted strategy name.
type execItem struct {
	rec          *core.ExecutionRecord
	strategyName string
}

// ─── Writer ───────────────────────────────────────────────────────────────────

// Writer is an asynchronous, batching DB writer.
// Goroutines call Write* methods (non-blocking); a background goroutine
// flushes batches every flushInterval or when a buffer reaches batchSize.
//
// If the Store is nil, all Write* calls are no-ops (graceful degradation when
// the database is not configured).
type Writer struct {
	s *Store

	// event channels
	execCh   chan execItem
	equityCh chan EquityRow
	riskCh   chan RiskRow
	statusCh chan StatusRow

	// positions: latest-value semantics (only the most recent snapshot matters)
	posMu     sync.Mutex
	posLatest []PosRow
	posDirty  bool

	flushInterval time.Duration
	batchSize     int

	done chan struct{}
	wg   sync.WaitGroup
}

// NewWriter creates a Writer but does not start it.
// Call Start() to launch the background goroutine.
func NewWriter(s *Store) *Writer {
	return &Writer{
		s:             s,
		execCh:        make(chan execItem, 2000),
		equityCh:      make(chan EquityRow, 2000),
		riskCh:        make(chan RiskRow, 500),
		statusCh:      make(chan StatusRow, 500),
		flushInterval: 150 * time.Millisecond,
		batchSize:     80,
		done:          make(chan struct{}),
	}
}

// Start launches the background flush goroutine.
func (w *Writer) Start() {
	if w.s == nil {
		return
	}
	w.wg.Add(1)
	go w.loop()
}

// Close signals the flush goroutine to stop and waits for a final flush.
func (w *Writer) Close() {
	if w.s == nil {
		return
	}
	close(w.done)
	w.wg.Wait()
}

// ─── Public write methods (non-blocking, safe to call from hot path) ──────────

// WriteExecution enqueues an execution record.  strategyName may be empty.
func (w *Writer) WriteExecution(rec *core.ExecutionRecord, strategyName string) {
	if w == nil || w.s == nil {
		return
	}
	select {
	case w.execCh <- execItem{rec: rec, strategyName: strategyName}:
	default:
		log.Printf("[Store] execCh full – dropped execution %s", rec.OrderID)
	}
}

// WriteEquityPoint enqueues one equity-curve point.
func (w *Writer) WriteEquityPoint(r EquityRow) {
	if w == nil || w.s == nil {
		return
	}
	select {
	case w.equityCh <- r:
	default:
		// equity curve: drop silently – just skip this tick
	}
}

// SyncPositions replaces the latest position snapshot.
// Thread-safe; only the most recent call per flush cycle takes effect.
func (w *Writer) SyncPositions(rows []PosRow) {
	if w == nil || w.s == nil {
		return
	}
	w.posMu.Lock()
	w.posLatest = rows
	w.posDirty = true
	w.posMu.Unlock()
}

// WriteRiskEvent enqueues a risk event.
func (w *Writer) WriteRiskEvent(r RiskRow) {
	if w == nil || w.s == nil {
		return
	}
	select {
	case w.riskCh <- r:
	default:
		log.Printf("[Store] riskCh full – dropped risk event %s", r.EventType)
	}
}

// WriteSystemStatus enqueues a system-status snapshot.
func (w *Writer) WriteSystemStatus(r StatusRow) {
	if w == nil || w.s == nil {
		return
	}
	select {
	case w.statusCh <- r:
	default:
		// status: drop silently – next tick will overwrite
	}
}

// ─── Background goroutine ─────────────────────────────────────────────────────

func (w *Writer) loop() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	var (
		execBuf   []execItem
		equityBuf []EquityRow
		riskBuf   []RiskRow
		statusBuf []StatusRow
	)

	flush := func() {
		if len(execBuf) > 0 {
			w.flushExec(execBuf)
			execBuf = execBuf[:0]
		}
		if len(equityBuf) > 0 {
			w.flushEquity(equityBuf)
			equityBuf = equityBuf[:0]
		}
		w.posMu.Lock()
		if w.posDirty {
			rows := make([]PosRow, len(w.posLatest))
			copy(rows, w.posLatest)
			w.posDirty = false
			w.posMu.Unlock()
			w.flushPositions(rows)
		} else {
			w.posMu.Unlock()
		}
		if len(riskBuf) > 0 {
			w.flushRisk(riskBuf)
			riskBuf = riskBuf[:0]
		}
		if len(statusBuf) > 0 {
			w.flushStatus(statusBuf)
			statusBuf = statusBuf[:0]
		}
	}

	for {
		select {
		case item := <-w.execCh:
			execBuf = append(execBuf, item)
			if len(execBuf) >= w.batchSize {
				w.flushExec(execBuf)
				execBuf = execBuf[:0]
			}

		case row := <-w.equityCh:
			equityBuf = append(equityBuf, row)
			if len(equityBuf) >= w.batchSize {
				w.flushEquity(equityBuf)
				equityBuf = equityBuf[:0]
			}

		case row := <-w.riskCh:
			riskBuf = append(riskBuf, row)

		case row := <-w.statusCh:
			statusBuf = append(statusBuf, row)

		case <-ticker.C:
			flush()

		case <-w.done:
			// Drain channels before exiting
			for {
				select {
				case item := <-w.execCh:
					execBuf = append(execBuf, item)
				case row := <-w.equityCh:
					equityBuf = append(equityBuf, row)
				case row := <-w.riskCh:
					riskBuf = append(riskBuf, row)
				case row := <-w.statusCh:
					statusBuf = append(statusBuf, row)
				default:
					goto drainDone
				}
			}
		drainDone:
			flush()
			return
		}
	}
}

// ─── Flush helpers ────────────────────────────────────────────────────────────

func retry(name string, attempts int, fn func() error) {
	for i := range attempts {
		if err := fn(); err == nil {
			return
		} else if i < attempts-1 {
			log.Printf("[Store] %s retry %d/%d: %v", name, i+1, attempts, err)
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
		} else {
			log.Printf("[Store] %s failed after %d attempts: %v – data dropped", name, attempts, err)
		}
	}
}

func (w *Writer) flushExec(items []execItem) {
	retry("flushExec", 3, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		batch := &pgx.Batch{}
		for _, it := range items {
			r := it.rec
			extra, _ := json.Marshal(map[string]any{
				"reason":    r.Reason,
				"fill_rate": r.FillRate,
			})
			batch.Queue(`
				INSERT INTO executions
					(order_id,symbol,side,qty,price,theoretical_price,slippage,status,
					 signal_time,execution_time,latency_ms,strategy_name,extra)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
				ON CONFLICT DO NOTHING`,
				r.OrderID, r.Symbol, r.Side, r.FilledQty,
				r.ActualPrice, r.TheoreticalPrice, r.SlippagePct, r.Status,
				r.SignalTime, r.ExecutionTime, r.Latency,
				it.strategyName, extra,
			)
		}
		br := w.s.pool.SendBatch(ctx, batch)
		defer br.Close()
		for range items {
			if _, err := br.Exec(); err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *Writer) flushEquity(rows []EquityRow) {
	retry("flushEquity", 3, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		batch := &pgx.Batch{}
		for _, r := range rows {
			batch.Queue(`
				INSERT INTO equity_curve (timestamp,equity,drawdown,cash,position_value)
				VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (timestamp) DO UPDATE
					SET equity=$2, drawdown=$3, cash=$4, position_value=$5`,
				r.Timestamp, r.Equity, r.Drawdown, r.Cash, r.PositionValue,
			)
		}
		br := w.s.pool.SendBatch(ctx, batch)
		defer br.Close()
		for range rows {
			if _, err := br.Exec(); err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *Writer) flushPositions(rows []PosRow) {
	retry("flushPositions", 3, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tx, err := w.s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx) //nolint:errcheck

		if _, err := tx.Exec(ctx, "DELETE FROM positions"); err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO positions (symbol,qty,avg_price,market_value,unrealized_pnl,updated_at)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				r.Symbol, r.Qty, r.AvgPrice, r.MarketValue, r.UnrealizedPnl, r.UpdatedAt,
			); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	})
}

func (w *Writer) flushRisk(rows []RiskRow) {
	retry("flushRisk", 3, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		batch := &pgx.Batch{}
		for _, r := range rows {
			batch.Queue(`
				INSERT INTO risk_events (timestamp,event_type,drawdown,position_pct,description)
				VALUES ($1,$2,$3,$4,$5)`,
				r.Timestamp, r.EventType, r.Drawdown, r.PositionPct, r.Description,
			)
		}
		br := w.s.pool.SendBatch(ctx, batch)
		defer br.Close()
		for range rows {
			if _, err := br.Exec(); err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *Writer) flushStatus(rows []StatusRow) {
	retry("flushStatus", 3, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		batch := &pgx.Batch{}
		for _, r := range rows {
			batch.Queue(`
				INSERT INTO system_status
					(timestamp,streak,risk_level,max_position_pct,
					 is_opening_allowed,is_kill_switch_active,anomaly_count)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (timestamp) DO UPDATE
					SET streak=$2, risk_level=$3, max_position_pct=$4,
					    is_opening_allowed=$5, is_kill_switch_active=$6, anomaly_count=$7`,
				r.Timestamp, r.Streak, r.RiskLevel, r.MaxPositionPct,
				r.AllowOpen, r.KillSwitchActive, r.AnomalyCount,
			)
		}
		br := w.s.pool.SendBatch(ctx, batch)
		defer br.Close()
		for range rows {
			if _, err := br.Exec(); err != nil {
				return err
			}
		}
		return nil
	})
}
