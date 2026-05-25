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

exec go run ./cmd/paper
