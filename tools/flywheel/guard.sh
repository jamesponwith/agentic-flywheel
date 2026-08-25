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
# Agent identity, in precedence order: the environment, then a file the
# spawner writes into the worktree. The file exists because an allowlist entry
# like Bash(tools/flywheel/guard.sh:*) does not match an env-prefixed
# invocation, so a permitted agent could not set FLYWHEEL_AGENT on the call and
# every audit entry it wrote said "unknown" — honest but degraded, and exactly
# the attribution weakness fw-7mw was about.
# --show-toplevel inside a worktree returns the WORKTREE, and .flywheel/STOP is
# gitignored so it never checks out there. Builders run only in worktrees
# (run.go sets cmd.Dir to one), so a switch thrown in the main tree was invisible
# to every agent it was meant to stop — verified: STOPPED in the main tree,
# runnable in a worktree of the same repo.
#
# --git-common-dir resolves to the ORIGINAL repo's .git for a linked worktree,
# so the switch is now checked where the human set it.
REPO_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
_common="$(git rev-parse --git-common-dir 2>/dev/null || echo)"
case "$_common" in
  "" | ".git") MAIN_REPO_DIR="$REPO_DIR" ;;
  *) MAIN_REPO_DIR="$(cd "$(dirname "$_common")" 2>/dev/null && pwd || echo "$REPO_DIR")" ;;
esac
REPO_STATE="$REPO_DIR/.flywheel"
LOG="$REPO_STATE/agent-log.jsonl"

# The ledger is also mirrored outside the working tree, because inside it the
# word "append-only" is not true. .flywheel/agent-log.jsonl is a tracked file,
# so `git checkout --`, `git reset --hard` and `git stash` all revert it like
# any other — and each of those is an ordinary thing to run while cleaning up.
# It happened twice in one session: once clearing test pollution, once syncing
# to origin, and both times the only genuine cost records went with it. Evidence
# that a routine command can erase is not evidence.
#
# The mirror is authoritative for recovery; the in-repo copy stays the shared,
# reviewable artifact. Under XDG state next to the builder identities, which is
# already the place for things git must not own.
MIRROR_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/flywheel/ledger"
MIRROR="$MIRROR_DIR/$(basename "$REPO_DIR")-agent-log.jsonl"
LEDGER="$REPO_STATE/review.jsonl"

usage() {
  cat <<'USAGE'
usage: guard.sh <command>

  check              exit 0 if agents may run, 1 if stopped (prints why)
  stop [reason]      stop every agent in this repo
  stop --fleet [r]   stop every agent in every repo
  resume [--fleet]   clear the corresponding stop file
  log <event> [k=v]  append an audit record ($FLYWHEEL_AGENT names the agent)
  restore-log        merge the out-of-tree mirror back into .flywheel/
  finding [k=v]      append a review finding to the review ledger
  status             show stop state and recent activity
USAGE
}

stop_file_repo="$REPO_STATE/STOP"
stop_file_fleet="$FLYWHEEL_HOME/STOP"

# agent_name resolves who is acting, without needing an env-prefixed call.
agent_name() {
  if [ -n "${FLYWHEEL_AGENT:-}" ]; then printf '%s' "$FLYWHEEL_AGENT"; return; fi
  if [ -r "$REPO_DIR/.flywheel/agent" ]; then head -1 "$REPO_DIR/.flywheel/agent"; return; fi
  printf 'unknown'
}

cmd_check() {
  if [ -f "$stop_file_fleet" ]; then
    echo "STOPPED (fleet-wide): $(cat "$stop_file_fleet")" >&2
    return 1
  fi
  if [ -f "$MAIN_REPO_DIR/.flywheel/STOP" ] && [ "$MAIN_REPO_DIR" != "$REPO_DIR" ]; then
    echo "STOPPED ($(basename "$MAIN_REPO_DIR"), set in the main worktree): $(cat "$MAIN_REPO_DIR/.flywheel/STOP")" >&2
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
  local record
  record=$(printf '{"ts":"%s","event":"%s","agent":"%s","repo":"%s"%s}' \
    "$(date -u +%FT%TZ)" "$(json_escape "$event")" \
    "$(json_escape "$(agent_name)")" "$(json_escape "$(basename "$REPO_DIR")")" \
    "$fields")
  printf '%s\n' "$record" >> "$LOG"
  # Mirror second and never fail the caller on it: a record that reached the
  # repo is already recorded, and losing the durable copy must not look like
  # losing the event.
  if mkdir -p "$MIRROR_DIR" 2>/dev/null; then
    printf '%s\n' "$record" >> "$MIRROR" 2>/dev/null || true
  fi
}

# restore-log — merge the out-of-tree mirror back into the repo copy.
#
# Union, not overwrite. The in-repo copy may hold records the mirror never saw
# (it predates the mirror), and the point is to lose nothing in either
# direction. sort -u over whole records is enough: each line carries its own
# ts, so two identical lines ARE the same event, and ordering by that leading
# field falls out of a plain lexicographic sort.
cmd_restore_log() {
  if [ ! -f "$MIRROR" ]; then
    echo "no mirror at $MIRROR" >&2; return 1
  fi
  mkdir -p "$REPO_STATE"; : >> "$LOG"
  local before merged
  # `|| echo 0` is wrong here: grep -c PRINTS 0 and then exits 1 on no match,
  # so the substitution captured "0", the echo appended another, and the
  # message came out as "restored: 0\n0 -> 2 records". Under set -e the bare
  # grep would abort instead, so it needs `|| :` rather than nothing.
  before=$(grep -c . "$LOG" 2>/dev/null || :)
  before=${before:-0}
  merged=$(mktemp)
  cat "$LOG" "$MIRROR" | grep -v '^[[:space:]]*$' | sort -u > "$merged"
  mv "$merged" "$LOG"
  after=$(grep -c . "$LOG" 2>/dev/null || :)
  echo "restored: $before -> ${after:-0} records"
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
  restore-log) shift; cmd_restore_log ;;
  status) shift; cmd_status ;;
  *)      usage; exit 2 ;;
esac
