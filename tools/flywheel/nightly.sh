#!/usr/bin/env bash
# The nightly fleet run (fw-lb8.3).
#
# This is NOT a GitHub Action. Builders are local agent sessions with local
# credentials and a local blackbird daemon; a hosted runner has none of those.
# So the schedule lives where the agents do — a systemd user timer on this
# machine, installed by `nightly.sh install`.
#
# Every bound is here rather than in the agent: the kill switch is checked
# first, the whole run has a wall-clock ceiling, and the digest is written
# somewhere a human will actually pass by.
set -euo pipefail
cd "$(git -C "$(dirname "$0")/../.." rev-parse --show-toplevel 2>/dev/null || echo "$HOME/Workspace/agentic-flywheel")"

ROSTER="${FLEET_ROSTER:-.flywheel/roster.json}"
DIGEST_DIR="${FLEET_DIGEST_DIR:-.flywheel/digests}"
RUN_CEILING="${FLEET_RUN_CEILING:-90m}"

# No default action. A bare invocation of a scheduler must not start one — the
# first test of this script launched a real fleet run because the default was
# "run", which is the same mistake as a dry-run flag defaulting to off.
case "${1:-help}" in
install)
  mkdir -p "$HOME/.config/systemd/user"
  cat > "$HOME/.config/systemd/user/flywheel-nightly.service" <<UNIT
[Unit]
Description=Flywheel nightly fleet run
[Service]
Type=oneshot
WorkingDirectory=$PWD
ExecStart=$PWD/tools/flywheel/nightly.sh run
UNIT
  cat > "$HOME/.config/systemd/user/flywheel-nightly.timer" <<UNIT
[Unit]
Description=Flywheel nightly fleet run
[Timer]
OnCalendar=*-*-* 02:30:00
Persistent=false
[Install]
WantedBy=timers.target
UNIT
  systemctl --user daemon-reload
  systemctl --user enable --now flywheel-nightly.timer
  echo "installed; next run: $(systemctl --user list-timers flywheel-nightly.timer --no-pager | sed -n 2p)"
  echo "stop it with: systemctl --user disable --now flywheel-nightly.timer"
  ;;

run)
  # The kill switch outranks the schedule. Checked before anything else, so
  # stopping the fleet never requires disabling a timer.
  if ! tools/flywheel/guard.sh check; then
    echo "nightly: halted by the kill switch"; exit 0
  fi
  mkdir -p "$DIGEST_DIR"
  stamp=$(date -u +%Y-%m-%dT%H%M%SZ)
  digest="$DIGEST_DIR/$stamp.md"

  # Gates first: a merged prerequisite should release its dependants before
  # the coordinator decides what is ready (ADR 0014).
  bd gate check >/dev/null 2>&1 || true

  {
    echo "# fleet run $stamp"
    echo
    timeout "$RUN_CEILING" go run ./tools/fleet run -roster "$ROSTER" -execute 2>&1 || \
      echo "(run exceeded $RUN_CEILING and was stopped)"
  } | tee "$digest"

  echo
  echo "digest: $digest"
  ;;

status)
  systemctl --user list-timers flywheel-nightly.timer --no-pager 2>/dev/null | sed -n 1,2p || echo "timer not installed"
  latest=$(ls -1t "$DIGEST_DIR"/*.md 2>/dev/null | head -1)
  if [ -n "$latest" ]; then echo; echo "latest digest: $latest"; tail -6 "$latest"; else echo "no digests yet"; fi
  ;;

help)
  cat <<'USAGE'
nightly.sh — the nightly fleet run

  install   enable the systemd user timer (02:30 daily)
  run       run the fleet once, now (this SPAWNS AGENTS)
  status    show the timer and the most recent digest

Never defaults to run: a bare invocation prints this.
USAGE
  ;;

*)
  echo "unknown: $1 — try 'help'" >&2; exit 2 ;;
esac
