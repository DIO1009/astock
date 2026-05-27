-- AStock Trading System — PostgreSQL Schema
-- All statements are idempotent (IF NOT EXISTS).
-- Run at startup via store.Migrate().

-- ─── executions ───────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS executions (
    id               BIGSERIAL PRIMARY KEY,
    order_id         TEXT             NOT NULL,
    symbol           TEXT             NOT NULL,
    side             TEXT             NOT NULL,
    qty              INT              NOT NULL,
    price            DOUBLE PRECISION NOT NULL,
    theoretical_price DOUBLE PRECISION NOT NULL,
    slippage         DOUBLE PRECISION NOT NULL DEFAULT 0,
    status           TEXT             NOT NULL,
    signal_time      BIGINT           NOT NULL DEFAULT 0,
    execution_time   BIGINT           NOT NULL,
    latency_ms       BIGINT           NOT NULL DEFAULT 0,
    strategy_name    TEXT             NOT NULL DEFAULT '',
    extra            JSONB
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_executions_event
    ON executions (order_id, execution_time, symbol, side);
CREATE INDEX IF NOT EXISTS idx_exec_sym_time ON executions (symbol, execution_time DESC);
CREATE INDEX IF NOT EXISTS idx_exec_time     ON executions (execution_time DESC);

-- ─── positions ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS positions (
    symbol          TEXT             PRIMARY KEY,
    qty             INT              NOT NULL,
    avg_price       DOUBLE PRECISION NOT NULL,
    market_value    DOUBLE PRECISION NOT NULL,
    unrealized_pnl  DOUBLE PRECISION NOT NULL,
    updated_at      BIGINT           NOT NULL
);

-- ─── equity_curve ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS equity_curve (
    timestamp       BIGINT           PRIMARY KEY,
    equity          DOUBLE PRECISION NOT NULL,
    drawdown        DOUBLE PRECISION NOT NULL DEFAULT 0,
    cash            DOUBLE PRECISION NOT NULL DEFAULT 0,
    position_value  DOUBLE PRECISION NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_equity_time ON equity_curve (timestamp DESC);

-- ─── risk_events ──────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS risk_events (
    id           BIGSERIAL        PRIMARY KEY,
    timestamp    BIGINT           NOT NULL,
    event_type   TEXT             NOT NULL,
    drawdown     DOUBLE PRECISION NOT NULL DEFAULT 0,
    position_pct DOUBLE PRECISION NOT NULL DEFAULT 0,
    description  TEXT             NOT NULL DEFAULT '',
    extra        JSONB
);
CREATE INDEX IF NOT EXISTS idx_risk_time ON risk_events (timestamp DESC);

-- ─── system_status ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS system_status (
    timestamp             BIGINT           PRIMARY KEY,
    streak                INT              NOT NULL DEFAULT 0,
    risk_level            TEXT             NOT NULL DEFAULT 'NORMAL',
    max_position_pct      DOUBLE PRECISION NOT NULL DEFAULT 0.8,
    is_opening_allowed    BOOLEAN          NOT NULL DEFAULT TRUE,
    is_kill_switch_active BOOLEAN          NOT NULL DEFAULT FALSE,
    anomaly_count         INT              NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_status_time ON system_status (timestamp DESC);

-- ─── orders (optional – future real-trading use) ──────────────────────────────
CREATE TABLE IF NOT EXISTS orders (
    order_id   TEXT             PRIMARY KEY,
    symbol     TEXT             NOT NULL,
    side       TEXT             NOT NULL,
    qty        INT              NOT NULL,
    price      DOUBLE PRECISION NOT NULL DEFAULT 0,
    status     TEXT             NOT NULL DEFAULT 'PENDING',
    created_at BIGINT           NOT NULL,
    updated_at BIGINT           NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_orders_sym  ON orders (symbol, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_time ON orders (created_at DESC);

-- ─── alpha_rankings ───────────────────────────────────────────────────────────
-- Written by daily_alpha job; read by screener/dynamic at runtime.
-- One row per (date, symbol); UPSERT on conflict.
CREATE TABLE IF NOT EXISTS alpha_rankings (
    id            BIGSERIAL        PRIMARY KEY,
    date          DATE             NOT NULL,
    symbol        TEXT             NOT NULL,
    name          TEXT             NOT NULL DEFAULT '',
    score         DOUBLE PRECISION NOT NULL DEFAULT 0,
    rank          INT              NOT NULL,
    -- raw factor values (for debugging / attribution)
    ret5d         DOUBLE PRECISION NOT NULL DEFAULT 0,
    ret20d        DOUBLE PRECISION NOT NULL DEFAULT 0,
    turnover      DOUBLE PRECISION NOT NULL DEFAULT 0,
    volume_ratio  DOUBLE PRECISION NOT NULL DEFAULT 0,
    mkt_cap       DOUBLE PRECISION NOT NULL DEFAULT 0,
    price         DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    UNIQUE (date, symbol)
);
CREATE INDEX IF NOT EXISTS idx_alpha_date_rank ON alpha_rankings (date DESC, rank ASC);
CREATE INDEX IF NOT EXISTS idx_alpha_date_sym  ON alpha_rankings (date DESC, symbol);

-- ─── daily_reports ────────────────────────────────────────────────────────────
-- One row per calendar date.  Written by report/scheduler at 15:10 CST daily.
-- status: PENDING → SUCCESS | FAILED
CREATE TABLE IF NOT EXISTS daily_reports (
    date         DATE        PRIMARY KEY,
    status       TEXT        NOT NULL DEFAULT 'PENDING', -- SUCCESS / FAILED / PENDING
    report_path  TEXT        NOT NULL DEFAULT '',        -- relative path to .md file
    generated_at TIMESTAMPTZ,
    error_msg    TEXT        NOT NULL DEFAULT '',
    retry_count  INT         NOT NULL DEFAULT 0
);
