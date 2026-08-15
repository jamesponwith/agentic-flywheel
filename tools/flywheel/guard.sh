#!/usr/bin/env bash
# guard.sh — the safety rail every unattended flywheel agent checks before it acts.
#
# Two jobs, both deliberately dumb enough to be trustworthy:
#   1. Kill switch  — a file. If it exists, no agent runs. Fleet-wide or per-repo.
#   2. Audit log    — append-only JSONL of what agents did (ADR 0003).
#
# Tamper-evidence is NOT this script's job: blackbird owns the authenticated
# event journal (ADR 0004). This log is the local, greppable convenience copy.
#
# ponytail: a file and an append. If this ever needs a daemon, it has failed.
set -euo pipefail

FLYWHEEL_HOME="${FLYWHEEL_HOME:-$HOME/.flywheel}"
REPO_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
REPO_STATE="$REPO_DIR/.flywheel"
LOG="$REPO_STATE/agent-log.jsonl"

usage() {
  cat <<'USAGE'
usage: guard.sh <command>

  check              exit 0 if agents may run, 1 if stopped (prints why)
  stop [reason]      stop every agent in this repo
  stop --fleet [r]   stop every agent in every repo
  resume [--fleet]   clear the corresponding stop file
  log <event> [k=v]  append an audit record
  status             show stop state and recent activity
USAGE
}

stop_file_repo="$REPO_STATE/STOP"
stop_file_fleet="$FLYWHEEL_HOME/STOP"

cmd_check() {
  if [ -f "$stop_file_fleet" ]; then
    echo "STOPPED (fleet-wide): $(cat "$stop_file_fleet")" >&2
    return 1
  fi
  if [ -f "$stop_file_repo" ]; then
    echo "STOPPED ($(basename "$REPO_DIR")): $(cat "$stop_file_repo")" >&2
    return 1
  fi
  return 0
}

cmd_stop() {
  local target="$stop_file_repo" scope="repo"
  if [ "${1:-}" = "--fleet" ]; then target="$stop_file_fleet"; scope="fleet"; shift; fi
  mkdir -p "$(dirname "$target")"
  printf '%s by %s at %s\n' "${*:-manual stop}" "${USER:-unknown}" "$(date -u +%FT%TZ)" > "$target"
  echo "stopped ($scope): $target"
  cmd_log agent.stopped "scope=$scope" "reason=${*:-manual stop}" || true
}

cmd_resume() {
  local target="$stop_file_repo" scope="repo"
  if [ "${1:-}" = "--fleet" ]; then target="$stop_file_fleet"; scope="fleet"; fi
  rm -f "$target"
  echo "resumed ($scope)"
  cmd_log agent.resumed "scope=$scope" || true
}

# log <event> [key=value ...] — values are JSON-escaped; unknown keys are fine.
cmd_log() {
  local event="${1:?event required}"; shift || true
  mkdir -p "$REPO_STATE"
  {
    printf '{"ts":"%s","event":"%s","agent":"%s","repo":"%s"' \
      "$(date -u +%FT%TZ)" "$event" "${FLYWHEEL_AGENT:-unknown}" "$(basename "$REPO_DIR")"
    for kv in "$@"; do
      local k="${kv%%=*}" v="${kv#*=}"
      v=${v//\\/\\\\}; v=${v//\"/\\\"}; v=${v//$'\n'/\\n}; v=${v//$'\t'/\\t}
      printf ',"%s":"%s"' "$k" "$v"
    done
    printf '}\n'
  } >> "$LOG"
}

cmd_status() {
  if cmd_check 2>/dev/null; then echo "state: RUNNABLE"; else echo "state: STOPPED"; cmd_check || true; fi
  echo "log:   $LOG"
  if [ -f "$LOG" ]; then
    echo "recent:"
    tail -5 "$LOG" | sed 's/^/  /'
  else
    echo "recent: (no activity)"
  fi
}

case "${1:-}" in
  check)  shift; cmd_check ;;
  stop)   shift; cmd_stop "$@" ;;
  resume) shift; cmd_resume "$@" ;;
  log)    shift; cmd_log "$@" ;;
  status) shift; cmd_status ;;
  *)      usage; exit 2 ;;
esac
