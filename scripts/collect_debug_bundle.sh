#!/usr/bin/env bash
# ============================================================
#  AStock — 诊断数据包收集脚本（供 AI / 人工排障分析）
#
#  用法：
#    bash scripts/collect_debug_bundle.sh
#    bash scripts/collect_debug_bundle.sh --lines 12000
#    bash scripts/collect_debug_bundle.sh --no-db --no-archive
#
#  输出：
#    backups/debug_bundle_YYYYMMDD_HHMMSS.tar.gz
#    （以及同名的解压目录，便于本地查看）
#
#  注意：config/server.env 中的数据库密码会被脱敏后再打包。
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

LOG_LINES=8000
JSONL_LINES=2000
INCLUDE_DB=1
MAKE_ARCHIVE=1
NOTE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --lines)
      LOG_LINES="${2:?missing value for --lines}"
      shift 2
      ;;
    --jsonl-lines)
      JSONL_LINES="${2:?missing value for --jsonl-lines}"
      shift 2
      ;;
    --no-db)
      INCLUDE_DB=0
      shift
      ;;
    --no-archive)
      MAKE_ARCHIVE=0
      shift
      ;;
    --note)
      NOTE="${2:?missing value for --note}"
      shift 2
      ;;
    -h|--help)
      sed -n '2,20p' "$0"
      exit 0
      ;;
    *)
      echo "[ERR] 未知参数: $1" >&2
      exit 1
      ;;
  esac
done

if [[ -f "${REPO_ROOT}/config/server.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${REPO_ROOT}/config/server.env"
  set +a
fi

STAMP="$(date +%Y%m%d_%H%M%S)"
BUNDLE_NAME="debug_bundle_${STAMP}"
OUT_DIR="${REPO_ROOT}/backups/${BUNDLE_NAME}"
ARCHIVE="${REPO_ROOT}/backups/${BUNDLE_NAME}.tar.gz"

mkdir -p "${OUT_DIR}"/{system,config,runtime,logs,reports,db,api}

redact_dsn() {
  echo "$1" | sed -E 's|(postgres(ql)?://[^:]+:)[^@]+(@)|\1***\2|g'
}

redact_env_file() {
  sed -E \
    -e 's|(postgres(ql)?://[^:]+:)[^@]+(@)|\1***\2|g' \
    -e 's/(PASSWORD=).*/\1***/Ig' \
    -e 's/(TOKEN=).*/\1***/Ig' \
    "$1"
}

copy_if_exists() {
  local src="$1"
  local dst="$2"
  if [[ -f "$src" ]]; then
    mkdir -p "$(dirname "$dst")"
    cp "$src" "$dst"
  fi
}

tail_if_exists() {
  local src="$1"
  local dst="$2"
  local lines="$3"
  if [[ -f "$src" ]]; then
    mkdir -p "$(dirname "$dst")"
    tail -n "$lines" "$src" >"$dst"
  fi
}

parse_dsn() {
  local dsn="$1"
  DSN_USER="$(echo "$dsn" | sed -E 's|.*://([^:]+):.*|\1|')"
  DSN_PASS="$(echo "$dsn" | sed -E 's|.*://[^:]+:([^@]+)@.*|\1|')"
  DSN_HOST="$(echo "$dsn" | sed -E 's|.*@([^:/]+).*|\1|')"
  DSN_PORT="$(echo "$dsn" | sed -E 's|.*:([0-9]+)/.*|\1|')"
  DSN_DB="$(echo "$dsn" | sed -E 's|.*/([^?]+).*|\1|')"
  export PGPASSWORD="$DSN_PASS"
}

echo "▶ 收集诊断数据包 → ${OUT_DIR}"

# ── 系统信息 ──────────────────────────────────────────────────────────────────
{
  echo "collected_at=$(date -Iseconds)"
  echo "hostname=$(hostname)"
  echo "timezone=$(timedatectl show -p Timezone --value 2>/dev/null || date +%Z)"
  echo "repo_root=${REPO_ROOT}"
  echo "user=$(whoami)"
  echo "note=${NOTE}"
} >"${OUT_DIR}/system/meta.env"

{
  uname -a
  echo "---"
  free -h 2>/dev/null || true
  echo "---"
  df -h "${REPO_ROOT}" 2>/dev/null || df -h
  echo "---"
  uptime
} >"${OUT_DIR}/system/host.txt"

{
  echo "go: $(go version 2>/dev/null || echo 'N/A')"
  echo "node: $(node -v 2>/dev/null || echo 'N/A')"
  echo "npm: $(npm -v 2>/dev/null || echo 'N/A')"
  echo "psql: $(psql --version 2>/dev/null || echo 'N/A')"
  go env GOPROXY GOSUMDB 2>/dev/null || true
} >"${OUT_DIR}/system/toolchain.txt"

if git -C "${REPO_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  {
    git -C "${REPO_ROOT}" rev-parse HEAD
    git -C "${REPO_ROOT}" rev-parse --abbrev-ref HEAD
    git -C "${REPO_ROOT}" status -sb
    git -C "${REPO_ROOT}" log -1 --format='%h %ci %s'
  } >"${OUT_DIR}/system/git.txt" 2>&1 || true
fi

# ── 进程与服务 ────────────────────────────────────────────────────────────────
{
  if pgrep -a paper_trader >/dev/null 2>&1; then
    echo "paper_trader_running=yes"
    pgrep -a paper_trader
  else
    echo "paper_trader_running=no"
  fi
  echo "---"
  if [[ -f "${REPO_ROOT}/scripts/pids" ]]; then
    echo "pid_file=$(cat "${REPO_ROOT}/scripts/pids")"
  fi
  systemctl is-active astock-paper 2>/dev/null || echo "systemd: N/A"
} >"${OUT_DIR}/system/process.txt"

# ── 配置（脱敏）──────────────────────────────────────────────────────────────
for f in config/server.env config/server.env.example config/trading_cost.json config/safety.json; do
  src="${REPO_ROOT}/${f}"
  dst="${OUT_DIR}/${f}"
  if [[ -f "$src" ]]; then
    mkdir -p "$(dirname "$dst")"
    if [[ "$f" == *server.env* ]]; then
      redact_env_file "$src" >"$dst"
    else
      cp "$src" "$dst"
    fi
  fi
done

{
  echo "ASTOCK_LIVE_DATA=${ASTOCK_LIVE_DATA:-}"
  echo "ASTOCK_TICK_SECONDS=${ASTOCK_TICK_SECONDS:-}"
  echo "ASTOCK_DYNAMIC_SCREENER=${ASTOCK_DYNAMIC_SCREENER:-}"
  echo "ASTOCK_TOP_N=${ASTOCK_TOP_N:-}"
  echo "ASTOCK_MAX_POS=${ASTOCK_MAX_POS:-}"
  echo "ASTOCK_ROTATION_ENABLED=${ASTOCK_ROTATION_ENABLED:-}"
  echo "ASTOCK_DB_DSN=$(redact_dsn "${ASTOCK_DB_DSN:-}")"
} >"${OUT_DIR}/config/runtime_env_snapshot.env"

# ── 运行时状态文件 ────────────────────────────────────────────────────────────
copy_if_exists "${REPO_ROOT}/scripts/pids" "${OUT_DIR}/runtime/pids"
tail_if_exists "${REPO_ROOT}/position_state.jsonl" "${OUT_DIR}/runtime/position_state.jsonl.tail" "$JSONL_LINES"
tail_if_exists "${REPO_ROOT}/paper_executions.jsonl" "${OUT_DIR}/runtime/paper_executions.jsonl.tail" "$JSONL_LINES"
tail_if_exists "${REPO_ROOT}/paper_trades.jsonl" "${OUT_DIR}/runtime/paper_trades.jsonl.tail" "$JSONL_LINES"

# 兼容旧文件名
for legacy in position_state.json paper_executions.json paper_trades.json; do
  copy_if_exists "${REPO_ROOT}/${legacy}" "${OUT_DIR}/runtime/${legacy}"
done

# ── 日志 ──────────────────────────────────────────────────────────────────────
shopt -s nullglob
LOG_FILES=( "${REPO_ROOT}"/logs/paper_trader*.log "${REPO_ROOT}"/logs/trader_*.log )
if [[ ${#LOG_FILES[@]} -gt 0 ]]; then
  ls -lt "${LOG_FILES[@]}" 2>/dev/null | head -5 >"${OUT_DIR}/logs/log_index.txt" || true
  latest="$(ls -t "${LOG_FILES[@]}" | head -1)"
  tail -n "$LOG_LINES" "$latest" >"${OUT_DIR}/logs/latest.log.tail"
  cp "${OUT_DIR}/logs/latest.log.tail" "${OUT_DIR}/logs/$(basename "$latest").tail"
  grep -E 'ERR|ERROR|失败|Fatal|panic|WARN|\[EastMoney\]|i/o timeout' \
    "${OUT_DIR}/logs/latest.log.tail" >"${OUT_DIR}/logs/errors_summary.txt" 2>/dev/null || true
else
  echo "no log files found under logs/" >"${OUT_DIR}/logs/log_index.txt"
fi

# ── 日报 ──────────────────────────────────────────────────────────────────────
if [[ -d "${REPO_ROOT}/reports" ]]; then
  ls -lt "${REPO_ROOT}"/reports/*.md 2>/dev/null | head -7 >"${OUT_DIR}/reports/index.txt" || true
  find "${REPO_ROOT}/reports" -maxdepth 1 -name '*.md' -type f -print0 2>/dev/null \
    | sort -z | tail -z -n 7 \
    | while IFS= read -r -d '' f; do
        cp "$f" "${OUT_DIR}/reports/$(basename "$f")"
      done
fi

# ── 本地 HTTP 快照（服务在跑时）──────────────────────────────────────────────
if curl -sf --connect-timeout 2 http://127.0.0.1:18099/health >/dev/null 2>&1; then
  curl -sf --connect-timeout 3 http://127.0.0.1:18099/health >"${OUT_DIR}/api/health.txt" || true
  for ep in positions equity executions risk-events; do
    curl -sf --connect-timeout 5 "http://127.0.0.1:18099/api/${ep}?limit=50" \
      >"${OUT_DIR}/api/${ep}.json" 2>/dev/null || true
  done
  curl -sf --connect-timeout 3 "http://127.0.0.1:18099/api/equity?range=7d" \
    >"${OUT_DIR}/api/equity_7d.json" 2>/dev/null || true
else
  echo "dashboard not reachable on :18099" >"${OUT_DIR}/api/health.txt"
fi

# ── PostgreSQL 导出（可选）──────────────────────────────────────────────────
DB_EXPORT_OK=no
if [[ "$INCLUDE_DB" -eq 1 ]] && command -v psql >/dev/null 2>&1; then
  DSN="${ASTOCK_DB_DSN:-}"
  if [[ -n "$DSN" && "$DSN" != "-" ]]; then
    parse_dsn "$DSN"
    if psql -h "$DSN_HOST" -p "$DSN_PORT" -U "$DSN_USER" -d "$DSN_DB" -c 'SELECT 1' >/dev/null 2>&1; then
      DB_EXPORT_OK=yes
      psql -h "$DSN_HOST" -p "$DSN_PORT" -U "$DSN_USER" -d "$DSN_DB" -Atc \
        "COPY (SELECT * FROM positions ORDER BY symbol) TO STDOUT WITH CSV HEADER" \
        >"${OUT_DIR}/db/positions.csv" 2>/dev/null || true
      psql -h "$DSN_HOST" -p "$DSN_PORT" -U "$DSN_USER" -d "$DSN_DB" -Atc \
        "COPY (SELECT * FROM equity_curve ORDER BY timestamp DESC LIMIT 2000) TO STDOUT WITH CSV HEADER" \
        >"${OUT_DIR}/db/equity_curve_recent.csv" 2>/dev/null || true
      psql -h "$DSN_HOST" -p "$DSN_PORT" -U "$DSN_USER" -d "$DSN_DB" -Atc \
        "COPY (SELECT * FROM executions ORDER BY execution_time DESC LIMIT 500) TO STDOUT WITH CSV HEADER" \
        >"${OUT_DIR}/db/executions_recent.csv" 2>/dev/null || true
      psql -h "$DSN_HOST" -p "$DSN_PORT" -U "$DSN_USER" -d "$DSN_DB" -Atc \
        "COPY (SELECT * FROM risk_events ORDER BY timestamp DESC LIMIT 200) TO STDOUT WITH CSV HEADER" \
        >"${OUT_DIR}/db/risk_events_recent.csv" 2>/dev/null || true
      psql -h "$DSN_HOST" -p "$DSN_PORT" -U "$DSN_USER" -d "$DSN_DB" -Atc \
        "COPY (SELECT * FROM system_status ORDER BY timestamp DESC LIMIT 100) TO STDOUT WITH CSV HEADER" \
        >"${OUT_DIR}/db/system_status_recent.csv" 2>/dev/null || true
      psql -h "$DSN_HOST" -p "$DSN_PORT" -U "$DSN_USER" -d "$DSN_DB" -Atc \
        "COPY (SELECT * FROM alpha_rankings WHERE date = (SELECT MAX(date) FROM alpha_rankings) ORDER BY rank ASC LIMIT 200) TO STDOUT WITH CSV HEADER" \
        >"${OUT_DIR}/db/alpha_rankings_latest.csv" 2>/dev/null || true
      psql -h "$DSN_HOST" -p "$DSN_PORT" -U "$DSN_USER" -d "$DSN_DB" -Atc \
        "COPY (SELECT * FROM daily_reports ORDER BY date DESC LIMIT 14) TO STDOUT WITH CSV HEADER" \
        >"${OUT_DIR}/db/daily_reports_recent.csv" 2>/dev/null || true
    else
      echo "psql connection failed" >"${OUT_DIR}/db/README.txt"
    fi
    unset PGPASSWORD
  else
    echo "ASTOCK_DB_DSN not configured" >"${OUT_DIR}/db/README.txt"
  fi
else
  echo "psql not installed or --no-db" >"${OUT_DIR}/db/README.txt"
fi

# ── AI 摘要 manifest ──────────────────────────────────────────────────────────
RUNNING=no
pgrep -x paper_trader >/dev/null 2>&1 && RUNNING=yes
ERR_COUNT=0
[[ -f "${OUT_DIR}/logs/errors_summary.txt" ]] && ERR_COUNT=$(wc -l <"${OUT_DIR}/logs/errors_summary.txt" | tr -d ' ')

cat >"${OUT_DIR}/README_FOR_AI.txt" <<EOF
AStock 诊断数据包
=================
收集时间: $(date -Iseconds)
主机: $(hostname)
服务运行: ${RUNNING}
Git: $(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo N/A)
用户备注: ${NOTE:-（无）}

目录说明:
  system/     主机、工具链、进程、git 状态
  config/     脱敏后的配置与环境变量快照
  runtime/    持仓快照与 jsonl 尾部
  logs/       最近日志尾部 + errors_summary.txt
  reports/    最近策略日报
  api/        本机 Dashboard HTTP 快照（若服务在跑）
  db/         PostgreSQL 关键表 CSV 导出（若可用）
  manifest.json  结构化摘要

建议 AI 优先阅读:
  1. manifest.json
  2. logs/errors_summary.txt
  3. logs/latest.log.tail
  4. runtime/position_state.jsonl.tail
  5. db/positions.csv / db/equity_curve_recent.csv
EOF

if command -v python3 >/dev/null 2>&1; then
  COLLECTED_AT="$(date -Iseconds)" python3 - <<'PY' "${OUT_DIR}" "${RUNNING}" "${DB_EXPORT_OK}" "${ERR_COUNT}" "${NOTE}"
import json, os, sys
out, running, db_ok, err_count, note = sys.argv[1:6]
manifest = {
    "bundle_type": "astock_debug_bundle",
    "collected_at": os.environ.get("COLLECTED_AT", ""),
    "service_running": running == "yes",
    "db_export_ok": db_ok == "yes",
    "log_error_line_count": int(err_count or 0),
    "user_note": note or "",
    "paths": {
        "errors_summary": "logs/errors_summary.txt",
        "latest_log": "logs/latest.log.tail",
        "position_state": "runtime/position_state.jsonl.tail",
        "executions": "runtime/paper_executions.jsonl.tail",
        "config_snapshot": "config/runtime_env_snapshot.env",
    },
    "analysis_hints": [
        "Compare position_state tail with db/positions.csv for drift.",
        "Check logs/errors_summary.txt for EastMoney EOF, DB errors, or safety freezes.",
        "If service_running=no, inspect system/process.txt and latest log tail for crash reason.",
    ],
}
files = []
for root, _, names in os.walk(out):
    for name in names:
        p = os.path.join(root, name)
        rel = os.path.relpath(p, out)
        try:
            files.append({"path": rel, "bytes": os.path.getsize(p)})
        except OSError:
            pass
manifest["files"] = sorted(files, key=lambda x: x["path"])
with open(os.path.join(out, "manifest.json"), "w", encoding="utf-8") as f:
    json.dump(manifest, f, ensure_ascii=False, indent=2)
PY
else
  cat >"${OUT_DIR}/manifest.json" <<EOF
{
  "bundle_type": "astock_debug_bundle",
  "service_running": "${RUNNING}",
  "db_export_ok": "${DB_EXPORT_OK}",
  "log_error_line_count": ${ERR_COUNT},
  "user_note": "${NOTE}"
}
EOF
fi

# ── 打包 ──────────────────────────────────────────────────────────────────────
if [[ "$MAKE_ARCHIVE" -eq 1 ]]; then
  mkdir -p "${REPO_ROOT}/backups"
  tar -czf "${ARCHIVE}" -C "${REPO_ROOT}/backups" "${BUNDLE_NAME}"
  ARCHIVE_SIZE=$(du -h "${ARCHIVE}" | awk '{print $1}')
  echo ""
  echo "============================================================"
  echo "  ✅ 诊断数据包已生成"
  echo ""
  echo "  目录: ${OUT_DIR}"
  echo "  压缩包: ${ARCHIVE} (${ARCHIVE_SIZE})"
  echo ""
  echo "  下载到本地 Mac（示例）:"
  echo "    scp -i ~/.ssh/你的密钥.pem ecs-user@服务器IP:${ARCHIVE} ~/Downloads/"
  echo ""
  echo "  交给 AI 分析时，可上传 ${BUNDLE_NAME}.tar.gz"
  echo "  或粘贴 logs/errors_summary.txt + manifest.json"
  echo "============================================================"
else
  echo "✅ 诊断数据目录: ${OUT_DIR}"
fi
