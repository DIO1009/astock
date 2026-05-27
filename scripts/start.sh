#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

: "${ASTOCK_ROTATION_ENABLED:=0}"
export ASTOCK_ROTATION_ENABLED

echo "[start.sh] ASTOCK_ROTATION_ENABLED=${ASTOCK_ROTATION_ENABLED}"
case "${ASTOCK_ROTATION_ENABLED}" in
  1|true|TRUE|yes|YES|on|ON)
    echo "[start.sh] rotation trading: enabled"
    ;;
  *)
    echo "[start.sh] rotation trading: disabled"
    ;;
esac

if ! command -v go >/dev/null 2>&1; then
  echo "[ERR] 未找到 go，请先安装 Go 并加入 PATH"
  exit 1
fi
if ! command -v caffeinate >/dev/null 2>&1; then
  echo "[ERR] 未找到 caffeinate（macOS 内置），无法防止休眠断网"
  exit 1
fi

mkdir -p logs bin

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

echo "▶ 编译 Go 后端..."
go build -o "$BIN" ./cmd/paper
echo "  ✓ 编译完成 → bin/paper_trader"

LOG_FILE="logs/paper_trader_$(date +%Y%m%d_%H%M%S).log"
echo "▶ 启动交易后端 (caffeinate -ims，休眠时保持运行与联网)..."
# caffeinate 为父进程；PID 文件记录其父进程 PID，stop.sh 对其发 SIGTERM 即可连带退出子进程
nohup caffeinate -ims "$BIN" >>"$LOG_FILE" 2>&1 &
PID="$!"
echo "$PID" >"$PID_FILE"

echo ""
echo "============================================================"
echo "  交易后端已启动"
echo "  PID (caffeinate): ${PID}"
echo "  二进制:           bin/paper_trader"
echo "  日志:             ${LOG_FILE}"
echo "  停止服务:         bash scripts/stop.sh"
echo "============================================================"
