#!/usr/bin/env bash
# ============================================================
#  AStock — Linux 云服务器：清空 Paper 数据并重新启动
#  用法：
#    bash scripts/reset_server.sh          # 交互确认后执行
#    bash scripts/reset_server.sh --yes  # 跳过确认（脚本/运维用）
#  前置：PostgreSQL 可用、已安装 psql 客户端、config/server.env 已配置
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

SKIP_CONFIRM=0
if [[ "${1:-}" == "--yes" ]]; then
  SKIP_CONFIRM=1
fi

# 与 start_server.sh 一致：优先加载 server.env
if [[ -f "${REPO_ROOT}/config/server.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${REPO_ROOT}/config/server.env"
  set +a
  echo "  ✓ 已加载 config/server.env"
fi

: "${ASTOCK_DB_DSN:=postgres://postgres:dmrxlbol123@127.0.0.1:5432/astock_trade?sslmode=disable}"
DB_DSN="${ASTOCK_DB_DSN}"

if [[ "$DB_DSN" == "-" ]]; then
  echo "[ERR] ASTOCK_DB_DSN='-' 时无法清空数据库，请先配置有效 DSN"
  exit 1
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "[ERR] 未找到 psql，请安装: sudo apt install -y postgresql-client"
  exit 1
fi

PGPASSWORD="$(echo "$DB_DSN" | sed -E 's|.*://[^:]+:([^@]+)@.*|\1|')"
PG_USER="$(echo  "$DB_DSN" | sed -E 's|.*://([^:]+):.*|\1|')"
PG_HOST="$(echo  "$DB_DSN" | sed -E 's|.*@([^:/]+).*|\1|')"
PG_PORT="$(echo  "$DB_DSN" | sed -E 's|.*:([0-9]+)/.*|\1|')"
PG_DB="$(echo    "$DB_DSN" | sed -E 's|.*/([^?]+).*|\1|')"
export PGPASSWORD

echo "============================================================"
echo "  AStock 云服务器 — 清空 Paper 数据并重跑"
echo "============================================================"
echo "  工作目录: ${REPO_ROOT}"
echo "  数据库:   ${PG_HOST}:${PG_PORT}/${PG_DB}"
echo ""
echo "  将执行："
echo "    1. 停止 paper_trader（scripts/stop.sh）"
echo "    2. TRUNCATE 全部业务表（含 alpha_rankings / daily_reports）"
echo "    3. 删除本地持仓快照、成交 jsonl、reports/、logs/*"
echo "    4. bash scripts/start_server.sh"
echo ""

if [[ "$SKIP_CONFIRM" -eq 0 ]]; then
  read -r -p "确认清空以上数据？[y/N] " ans
  case "${ans}" in
    y|Y|yes|YES) ;;
    *)
      echo "已取消"
      exit 0
      ;;
  esac
fi

echo ""
echo "▶ 停止交易后端"
bash "${SCRIPT_DIR}/stop.sh"

echo "▶ 清空数据库表"
psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 <<'SQL'
TRUNCATE TABLE
  executions,
  positions,
  equity_curve,
  risk_events,
  system_status,
  orders,
  alpha_rankings,
  daily_reports
RESTART IDENTITY;
SQL

echo "▶ 删除本地运行状态"
rm -f \
  "${REPO_ROOT}/paper_executions.json" \
  "${REPO_ROOT}/paper_executions.jsonl" \
  "${REPO_ROOT}/paper_trades.json" \
  "${REPO_ROOT}/paper_trades.jsonl" \
  "${REPO_ROOT}/position_state.json" \
  "${REPO_ROOT}/position_state.jsonl"

echo "▶ 清空报告与日志"
rm -rf "${REPO_ROOT}/reports"
mkdir -p "${REPO_ROOT}/reports"
find "${REPO_ROOT}/logs" -type f -delete 2>/dev/null || true

echo "▶ 重新启动（start_server.sh）"
exec bash "${SCRIPT_DIR}/start_server.sh"
