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

mkdir -p logs

PID_FILE="${REPO_ROOT}/scripts/pids"
if [[ -f "$PID_FILE" ]]; then
  if kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "[start.sh] paper trader already running: pid=$(cat "$PID_FILE")"
    exit 0
  fi
  rm -f "$PID_FILE"
fi

LOG_FILE="logs/paper_trader_$(date +%Y%m%d_%H%M%S).log"
nohup go run ./cmd/paper >"$LOG_FILE" 2>&1 &
PID="$!"
echo "$PID" >"$PID_FILE"

echo "[start.sh] paper trader started: pid=${PID}"
echo "[start.sh] log: ${LOG_FILE}"
