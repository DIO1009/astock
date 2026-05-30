# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

### Build & Run

```bash
# Build and start the paper trader (main entry point)
bash scripts/start.sh

# Stop the paper trader
bash scripts/stop.sh

# Build only (outputs to bin/paper_trader)
go build -o bin/paper_trader ./cmd/paper

# Run the demo simulation (main.go — 200-tick mock run, no DB/dashboard)
go run .

# Start with live market data (East Money push2 API, 5-min tick)
ASTOCK_LIVE_DATA=1 bash scripts/start.sh

# Start with dynamic screener (Top-100 by alpha score, max 10 positions)
ASTOCK_LIVE_DATA=1 ASTOCK_DYNAMIC_SCREENER=1 ASTOCK_MAX_POS=10 bash scripts/start.sh
```

### Tests

```bash
# Run all tests
go test ./...

# Run a specific package's tests
go test ./engine/...
go test ./safety/...
go test ./rotation/...

# Run a single test by name
go test ./engine/ -run TestQuoteProviderFallback -v

# Run tests in cmd/paper
go test ./cmd/paper/ -run TestProtectionScope -v
```

### Data

```bash
# Download ~120 days of real A-share price data (requires akshare)
python3 scripts/fetch_data.py

# Custom symbols / days
python3 scripts/fetch_data.py --symbols 600519 000858 300750 000300 --days 120
```

### Dashboard (frontend only, for local dev)

```bash
cd dashboard/frontend
npm install
npm run dev    # dev server
npm run build  # production build → dist/
```

## Architecture

### Entry Points

There are two composition roots:

- **`main.go`** — 200-tick mock simulation using `provider/mock`. No DB, no dashboard. Used for fast algorithm verification.
- **`cmd/paper/main.go`** — Production paper-trading entry point. Uses `provider/replay` (CSV) or `provider/eastmoney` (live), connects to PostgreSQL, starts the dashboard HTTP server on `:18099`.

Both wire up the same `engine.Engine` with different concrete implementations.

### Interface Contract Layer (`core/`)

All module boundaries are defined as Go interfaces in `core/interfaces.go`. `core/types.go` holds all shared domain types (`Quote`, `Position`, `Order`, `Trade`, `Signal`, etc.). **Nothing in `core/` depends on any other package in this repo.** Every other package depends on `core`.

Key interfaces and their roles:
- `DataProvider` — fetches real-time level-1 quote snapshots
- `AlphaStrategy` / `AlphaEngine` — scores symbols on `[-1, +1]`; `AlphaEngine.Rank` returns sorted `[]Signal`
- `StrategyRegistry` — extends `AlphaEngine` with per-strategy performance attribution and dynamic weight adjustment (detected via type assertion in the engine)
- `PortfolioDecision` — sole authority for generating BUY orders; SELL is owned by `PositionManager`
- `PositionManager` — owns the position book; all mutations go through `ApplyTrade`
- `ExecController` — enforces execution discipline (cooldown, high-price re-entry block, min hold ticks, per-tick buy/sell caps)
- `SafetyGuard` — final safety layer: losing-streak suppression, manual operator controls (SIGUSR1/SIGUSR2), execution anomaly detection
- `Monitor` — tracks equity/drawdown/risk level and fires alert callbacks
- `DashboardReporter` — called by the engine at end of each tick; pushes data to WebSocket clients

Optional components are injected via setters (`engine.SetMonitor`, `engine.SetSafetyGuard`, `engine.SetDashboard`, etc.) rather than constructor parameters.

### Engine Tick Loop (`engine/engine.go`)

Each tick runs in phases:
1. `AdvanceTick` on `ExecController` and `SafetyGuard`
2. `ShouldForceLiquidate` check (SafetyGuard)
3. Fetch quotes from `DataProvider`; update market filter state
4. Run `AlphaEngine.Rank` → `SignalAdjuster.Adjust` → `SignalStabilizer.Stabilize`
5. Generate SELL orders via `PositionManager.CheckExit` (filtered by `ExecController.AllowSell`)
6. Generate BUY orders via `PortfolioDecision.Decide` (gated by market filter + `SafetyGuard.AllowOpen` + `AdaptiveOptimizer`)
7. Execute orders via `Executor`; apply trades to `PositionManager`; record via `PerformanceTracker`
8. Compute equity; call `Monitor.Update`; call `DashboardReporter.OnTick`

**T+1 enforcement**: positions bought today have `SellableQty = 0`; `PositionManager.AdvanceTradeDay` unlocks them at the start of the next trading day.

### Alpha Strategy Layer (`alpha/`)

Five orthogonal strategies registered in `alpha/registry`:
- `momentum` — multi-period trend (5d + 20d returns)
- `reversal` — mean reversion via EMA deviation
- `breakout` — volume-confirmed price breakout
- `volume` — relative volume as institutional participation proxy
- `volatility` — pure risk penalty factor (scores in `[-1, 0]`)

The registry updates weights every 20 ticks based on attributed PnL (dynamic weight bounded by `MinFactor`/`MaxFactor`). `AdaptiveOptimizer` separately adjusts `MaxTotalPct` (position sizing) and `BuyThreshold` (entry score gate) based on drawdown and win-rate.

Signal pipeline: `Rank` → `SignalAdjuster` (anti-monopoly dampening) → `SignalStabilizer` (require N consecutive ticks in top-N before promoting to stable BUY candidate).

### Data Providers (`provider/`)

- `provider/mock` — deterministic random prices; used by `main.go`
- `provider/replay` — replays `real_market_data.csv` (or `ASTOCK_DATA_PATH`); derived fields (`Return5d`, `EMA20`, `Volatility`, etc.) are computed here, not in strategies
- `provider/eastmoney` — live Level-1 quotes from East Money push2 API; activated by `ASTOCK_LIVE_DATA=1`
- `provider/stress` — stress-testing provider

**Invariant**: derived fields on `core.Quote` (`Return5d`, `Return20d`, `EMA20`, `Volatility`, `AvgVolume5d`, `VolumeRatio`) must be populated by the `DataProvider`, never by individual strategies.

### Dashboard (`dashboard/`)

Go HTTP server in `dashboard/server.go` serves:
- Static frontend from `dashboard/frontend/dist/`
- WebSocket at `/ws` for real-time push
- REST API at `/api/equity`, `/api/executions`, `/api/positions`, `/api/risk-events`, `/api/system-status/latest`

Frontend is React + TypeScript + Vite + Tailwind + Recharts.

### Persistence (`store/`)

PostgreSQL via `pgx/v5`. Schema is auto-migrated at startup via `store.Migrate()`. Tables: `executions`, `positions`, `equity_curve`, `risk_events`, `system_status`, `orders`, `alpha_rankings`, `daily_reports`.

Default DSN: `postgres://postgres:dmrxlbol123@127.0.0.1:5432/astock_trade`  
Override: `ASTOCK_DB_DSN` env var. Set to `-` to disable DB entirely.

Position state is also persisted to `position_state.jsonl` on graceful shutdown and restored at startup.

### Key Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `ASTOCK_LIVE_DATA` | `0` | `1` = East Money live API |
| `ASTOCK_TICK_SECONDS` | `300` | Tick interval in seconds |
| `ASTOCK_DYNAMIC_SCREENER` | `0` | `1` = dynamic alpha-based screener |
| `ASTOCK_TOP_N` | `100` | Number of top stocks for dynamic screener |
| `ASTOCK_MAX_POS` | `10` | Max concurrent positions |
| `ASTOCK_ROTATION_ENABLED` | `0` | Enable sector rotation policy |
| `ASTOCK_DATA_PATH` | — | Override CSV data file path |
| `ASTOCK_DB_DSN` | (postgres default) | PostgreSQL connection string |

### Operator Controls (live)

```bash
PID=$(cat scripts/pids)
kill -USR1 $PID   # Stop opening new positions
kill -HUP  $PID   # Resume opening
kill -USR2 $PID   # Force-liquidate all positions
```
