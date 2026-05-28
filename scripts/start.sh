#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

# ── 环境变量默认值 ────────────────────────────────────────────────────────────
: "${ASTOCK_LIVE_DATA:=0}"
: "${ASTOCK_TICK_SECONDS:=300}"
: "${ASTOCK_DYNAMIC_SCREENER:=0}"
: "${ASTOCK_TOP_N:=100}"
: "${ASTOCK_MAX_POS:=10}"
: "${ASTOCK_ROTATION_ENABLED:=0}"
: "${ASTOCK_DB_DSN:=postgres://postgres:dmrxlbol123@127.0.0.1:5432/astock_trade?sslmode=disable}"
export ASTOCK_LIVE_DATA ASTOCK_TICK_SECONDS ASTOCK_DYNAMIC_SCREENER
export ASTOCK_TOP_N ASTOCK_MAX_POS ASTOCK_ROTATION_ENABLED ASTOCK_DB_DSN

# ── 运行模式摘要 ──────────────────────────────────────────────────────────────
echo ""
echo "▶ 运行模式:"
if [[ "${ASTOCK_LIVE_DATA}" == "1" ]]; then
  echo "  数据源:   东方财富实时行情"
else
  echo "  数据源:   本地 CSV / 合成数据（回放模式）"
fi

TICK_HUMAN="${ASTOCK_TICK_SECONDS}s"
if [[ "${ASTOCK_TICK_SECONDS}" -ge 60 ]]; then
  TICK_HUMAN="$((ASTOCK_TICK_SECONDS / 60))m$((ASTOCK_TICK_SECONDS % 60 == 0 ? 0 : ASTOCK_TICK_SECONDS % 60))s"
  # 整分钟更简洁
  if (( ASTOCK_TICK_SECONDS % 60 == 0 )); then
    TICK_HUMAN="$((ASTOCK_TICK_SECONDS / 60))m"
  fi
fi
echo "  Tick 间隔: ${TICK_HUMAN}"

if [[ "${ASTOCK_DYNAMIC_SCREENER}" == "1" ]]; then
  echo "  选股模式:  动态选股（Top-${ASTOCK_TOP_N}，最大持仓 ${ASTOCK_MAX_POS}）"
else
  echo "  选股模式:  静态股票池（最大持仓 ${ASTOCK_MAX_POS}）"
fi

case "${ASTOCK_ROTATION_ENABLED}" in
  1|true|TRUE|yes|YES|on|ON)
    echo "  轮动策略:  已启用"
    ;;
  *)
    echo "  轮动策略:  已禁用"
    ;;
esac

# ── 依赖检查 ──────────────────────────────────────────────────────────────────
echo ""
if ! command -v go >/dev/null 2>&1; then
  echo "[ERR] 未找到 go，请先安装 Go 并加入 PATH"
  exit 1
fi
if ! command -v caffeinate >/dev/null 2>&1; then
  echo "[ERR] 未找到 caffeinate（macOS 内置），无法防止休眠断网"
  exit 1
fi

# ── 重复启动检查 ──────────────────────────────────────────────────────────────
BIN="${REPO_ROOT}/bin/paper_trader"
PID_FILE="${REPO_ROOT}/scripts/pids"

if [[ -f "$PID_FILE" ]]; then
  OLD_PID="$(cat "$PID_FILE")"
  if [[ -n "$OLD_PID" ]] && kill -0 "$OLD_PID" 2>/dev/null; then
    echo "[start.sh] 交易后端已在运行: pid=${OLD_PID}"
    exit 0
  fi
  rm -f "$PID_FILE"
fi
if pgrep -x "paper_trader" >/dev/null 2>&1; then
  echo "[ERR] 发现残留 paper_trader 进程，请先执行: bash scripts/stop.sh"
  pgrep -xl "paper_trader" || true
  exit 1
fi

mkdir -p logs bin

# ── 数据库连接提示 ────────────────────────────────────────────────────────────
echo "▶ 数据库..."
if [[ "${ASTOCK_DB_DSN}" == "-" ]]; then
  echo "  [跳过] ASTOCK_DB_DSN='-'，数据库已禁用"
else
  echo "  ${ASTOCK_DB_DSN}"
fi

# ── 构建前端 ──────────────────────────────────────────────────────────────────
echo "▶ 构建前端..."
FRONTEND_DIR="${REPO_ROOT}/dashboard/frontend"
if ! command -v npm >/dev/null 2>&1; then
  echo "  [WARN] 未找到 npm，跳过前端构建（使用现有 dist/）"
elif [[ ! -d "${FRONTEND_DIR}" ]]; then
  echo "  [WARN] 未找到 ${FRONTEND_DIR}，跳过前端构建"
else
  (cd "${FRONTEND_DIR}" && npm install --silent && npm run build)
  echo "  ✓ 前端构建完成 → dashboard/frontend/dist/"
fi

# ── 编译 Go 后端 ──────────────────────────────────────────────────────────────
echo "▶ 编译 Go 后端..."
go build -o "$BIN" ./cmd/paper
echo "  ✓ 编译完成 → bin/paper_trader"

# ── 启动 ──────────────────────────────────────────────────────────────────────
DASHBOARD_PORT="18099"
LOG_FILE="logs/paper_trader_$(date +%Y%m%d_%H%M%S).log"
echo "▶ 启动交易后端 (caffeinate -ims，休眠时保持运行与联网)..."
# caffeinate 为父进程；PID 文件记录其 PID，stop.sh 对其发 SIGTERM 即可连带退出子进程
nohup caffeinate -ims "$BIN" >>"$LOG_FILE" 2>&1 &
PID="$!"
echo "$PID" >"$PID_FILE"

# 等一秒确认进程存活
sleep 1
if ! kill -0 "$PID" 2>/dev/null; then
  echo "[ERR] 进程启动失败，查看日志: ${LOG_FILE}"
  exit 1
fi
echo "  ✓ 进程启动成功 (PID=${PID})"

echo ""
echo "============================================================"
echo "  ✅ A股量化交易系统已启动"
echo ""
echo "  浏览器:     http://localhost:${DASHBOARD_PORT}"
echo "  实时日志:   tail -f ${REPO_ROOT}/${LOG_FILE}"
echo "  停止服务:   bash scripts/stop.sh"
echo ""
echo "  [API 查询]"
echo "    http://localhost:${DASHBOARD_PORT}/api/equity?range=7d"
echo "    http://localhost:${DASHBOARD_PORT}/api/executions?limit=50"
echo "    http://localhost:${DASHBOARD_PORT}/api/positions"
echo "    http://localhost:${DASHBOARD_PORT}/api/risk-events"
echo ""
if [[ "${ASTOCK_DYNAMIC_SCREENER}" == "1" ]]; then
echo "  [动态选股调度]"
echo "    每天 09:30 CST 自动运行选股（Top-${ASTOCK_TOP_N}）"
echo "    09:30–15:00 CST 交易时段按 ${TICK_HUMAN} tick 自动轮换持仓"
echo "    选股完成后 Screener 自动刷新，新持仓将在下一 Tick 生效"
echo ""
fi
echo "  [人工干预]"
echo "    kill -USR1 ${PID}  → 停止开仓"
echo "    kill -USR2 ${PID}  → 全部清仓"
echo "    kill -HUP  ${PID}  → 恢复开仓"
echo "============================================================"
