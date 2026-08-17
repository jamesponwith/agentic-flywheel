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
LEDGER="$REPO_STATE/review.jsonl"

usage() {
  cat <<'USAGE'
usage: guard.sh <command>

  check              exit 0 if agents may run, 1 if stopped (prints why)
  stop [reason]      stop every agent in this repo
  stop --fleet [r]   stop every agent in every repo
  resume [--fleet]   clear the corresponding stop file
  log <event> [k=v]  append an audit record ($FLYWHEEL_AGENT names the agent)
  finding [k=v]      append a review finding to the review ledger
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

# Fields every record writes for itself. A caller-supplied duplicate would emit
# two keys of one name — last-wins in lenient parsers, rejected by strict ones,
# and either way a log that misstates who acted. ADR 0003 makes this the record
# of what unattended agents did, so a bad pair is refused, never guessed at.
log_reserved="ts event agent repo"
finding_reserved="ts commit branch repo"

# ponytail: the five escapes JSON needs from shell-sourced text. Other control
# characters (0x00-0x1f) would still produce an invalid record; covering them
# needs a per-character loop, which is worth writing the day one turns up.
json_escape() {
  local s=$1
  s=${s//\\/\\\\}; s=${s//\"/\\\"}
  s=${s//$'\n'/\\n}; s=${s//$'\r'/\\r}; s=${s//$'\t'/\\t}
  printf '%s' "$s"
}

# json_pairs <reserved> [k=v ...] — validate every pair, then print the JSON
# fragment. Nothing is printed until all of them pass: a refusal that had
# already emitted half a record would corrupt the log it exists to protect.
json_pairs() {
  local reserved=" $1 "; shift
  local kv k v out=""
  for kv in "$@"; do
    if [ "${kv#*=}" = "$kv" ]; then
      echo "guard.sh: '$kv' is not key=value" >&2
      return 2
    fi
    k="${kv%%=*}" v="${kv#*=}"
    if [ -z "$k" ]; then
      echo "guard.sh: empty key in '$kv'" >&2
      return 2
    fi
    case "$reserved" in
      *" $k "*)
        echo "guard.sh: '$k' is written automatically and cannot be passed" >&2
        if [ "$k" = agent ]; then
          echo "guard.sh: set FLYWHEEL_AGENT instead" >&2
        fi
        return 2
        ;;
    esac
    out+=",\"$(json_escape "$k")\":\"$(json_escape "$v")\""
  done
  printf '%s' "$out"
}

# log <event> [key=value ...] — the agent comes from $FLYWHEEL_AGENT, not a pair.
cmd_log() {
  local event="${1:?event required}"; shift || true
  local fields
  fields=$(json_pairs "$log_reserved" "$@") || return 2
  mkdir -p "$REPO_STATE"
  printf '{"ts":"%s","event":"%s","agent":"%s","repo":"%s"%s}\n' \
    "$(date -u +%FT%TZ)" "$(json_escape "$event")" \
    "$(json_escape "${FLYWHEEL_AGENT:-unknown}")" "$(json_escape "$(basename "$REPO_DIR")")" \
    "$fields" >> "$LOG"
}

# finding — append one review finding to .flywheel/review.jsonl.
#
# The ledger is the point: a review whose findings are never recorded can
# never be shown to be worth its cost. Each finding gets a disposition later
# (accepted | rejected | ignored) and, once Learn v2 lands, an escape verdict —
# did anything the reviewer missed turn up in CI or production?
#
# Expected keys: lens, file, line, severity, claim, disposition.
cmd_finding() {
  local fields
  fields=$(json_pairs "$finding_reserved" "$@") || return 2
  mkdir -p "$REPO_STATE"
  printf '{"ts":"%s","commit":"%s","branch":"%s","repo":"%s"%s}\n' \
    "$(date -u +%FT%TZ)" \
    "$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)" \
    "$(git -C "$REPO_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)" \
    "$(basename "$REPO_DIR")" "$fields" >> "$LEDGER"
}

cmd_status() {
  if cmd_check 2>/dev/null; then echo "state: RUNNABLE"; else echo "state: STOPPED"; cmd_check || true; fi
  echo "log:   $LOG"
  if [ -f "$LEDGER" ]; then echo "review: $(wc -l < "$LEDGER" | tr -d ' ') findings recorded"; fi
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
  finding) shift; cmd_finding "$@" ;;
  status) shift; cmd_status ;;
  *)      usage; exit 2 ;;
esac
