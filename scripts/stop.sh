#!/usr/bin/env bash
# ============================================================
#  AStock Trading Cockpit — 停止脚本
#  用法：bash scripts/stop.sh
# ============================================================

set -euo pipefail

WORKSPACE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PID_FILE="$WORKSPACE/scripts/pids"

if [[ ! -f "$PID_FILE" ]]; then
    echo "[INFO] 未找到 PID 文件，服务可能未在运行"
    # 兜底：尝试按进程名终止
    if pgrep -x "paper_trader" > /dev/null 2>&1; then
        echo "  发现残留进程，正在终止..."
        pkill -x "paper_trader" || true
        echo "  ✓ 已终止"
    fi
    exit 0
fi

PID=$(cat "$PID_FILE")

if kill -0 "$PID" 2>/dev/null; then
    echo "▶ 正在停止交易后端 (PID=$PID)..."

    # 先发 SIGTERM，给 Go 进程做优雅退出（保存持仓快照）
    kill -TERM "$PID" 2>/dev/null || true
    sleep 3

    # 若仍存活则强制终止
    if kill -0 "$PID" 2>/dev/null; then
        echo "  [WARN] 进程未响应 SIGTERM，发送 SIGKILL..."
        kill -KILL "$PID" 2>/dev/null || true
        sleep 1
    fi

    # caffeinate 是父进程，也一并清理
    # (nohup 下 caffeinate 是 paper_trader 的父进程，SIGTERM 后应已退出)
    pkill -P "$PID" 2>/dev/null || true

    echo "  ✓ 已停止"
else
    echo "[INFO] 进程 $PID 已不存在"
fi

rm -f "$PID_FILE"

# 兜底：确保没有残留 paper_trader 进程
if pgrep -x "paper_trader" > /dev/null 2>&1; then
    pkill -x "paper_trader" || true
    echo "  清理残留进程完成"
fi

echo "============================================================"
echo "  服务已停止。持仓快照保存于: position_state.json"
echo "  查看日志: ls -lt logs/ | head -5"
echo "============================================================"
