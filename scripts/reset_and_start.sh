#!/usr/bin/env bash
set -euo pipefail

WORKSPACE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$WORKSPACE"

PID_FILE="$WORKSPACE/scripts/pids"
DB_DSN="${ASTOCK_DB_DSN:-postgres://postgres:dmrxlbol123@127.0.0.1:5432/astock_trade?sslmode=disable}"

if [[ "${ASTOCK_DB_DSN:-}" == "-" ]]; then
  echo "[ERR] ASTOCK_DB_DSN='-' 时无法清空数据库"
  exit 1
fi

PGPASSWORD="$(echo "$DB_DSN" | sed -E 's|.*://[^:]+:([^@]+)@.*|\1|')"
PG_USER="$(echo  "$DB_DSN" | sed -E 's|.*://([^:]+):.*|\1|')"
PG_HOST="$(echo  "$DB_DSN" | sed -E 's|.*@([^:/]+).*|\1|')"
PG_PORT="$(echo  "$DB_DSN" | sed -E 's|.*:([0-9]+)/.*|\1|')"
PG_DB="$(echo    "$DB_DSN" | sed -E 's|.*/([^?]+).*|\1|')"
export PGPASSWORD

echo "============================================================"
echo "  AStock 清空并重跑"
echo "============================================================"
echo "DB: $PG_HOST:$PG_PORT/$PG_DB"
echo ""

if [[ -f "$PID_FILE" ]]; then
  OLD_PID=$(cat "$PID_FILE")
  if kill -0 "$OLD_PID" 2>/dev/null; then
    echo "▶ 停止当前后端 (PID=$OLD_PID)"
    kill -TERM "$OLD_PID" 2>/dev/null || true
    sleep 3
    kill -0 "$OLD_PID" 2>/dev/null && kill -KILL "$OLD_PID" 2>/dev/null || true
  fi
  rm -f "$PID_FILE"
fi
pkill -x "paper_trader" 2>/dev/null || true

echo "▶ 清空数据库表"
psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 <<'SQL'
TRUNCATE TABLE
  executions,
  positions,
  equity_curve,
  risk_events,
  system_status,
  alpha_rankings
RESTART IDENTITY;
SQL

echo "▶ 删除本地运行状态"
rm -f \
  "$WORKSPACE/paper_executions.json" \
  "$WORKSPACE/paper_executions.jsonl" \
  "$WORKSPACE/paper_trades.json" \
  "$WORKSPACE/paper_trades.jsonl" \
  "$WORKSPACE/position_state.json" \
  "$WORKSPACE/position_state.jsonl"

echo "▶ 清空报告与日志"
rm -rf "$WORKSPACE/reports"
mkdir -p "$WORKSPACE/reports"
find "$WORKSPACE/logs" -type f -delete 2>/dev/null || true

echo "▶ 重新启动"
exec bash "$WORKSPACE/scripts/start.sh"
