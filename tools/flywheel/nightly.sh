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
# The installer's PATH, captured. A systemd user service otherwise gets a
# minimal one with no go, no bd and no gh, and the run dies before it starts —
# which is exactly how the first unattended night was lost.
Environment=PATH=$PATH
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
  # Exit 1 is "stopped", and only exit 1. Anything else means guard.sh could
  # not reach a verdict — a broken PATH, a missing file, a syntax error — and
  # reporting that as a deliberate halt while exiting 0 turns an outage into a
  # clean night. Found by a test whose PATH was too small to run guard.sh at
  # all, which then reported the kill switch as set on a repo where it wasn't.
  gc=0; tools/flywheel/guard.sh check || gc=$?
  case "$gc" in
  0) ;;
  1) echo "nightly: halted by the kill switch"; exit 0 ;;
  *) echo "nightly: guard.sh could not answer (exit $gc) — refusing to run agents" >&2
     exit "$gc" ;;
  esac
  # A timer's PATH is not a login shell's. Fail here, by name, rather than
  # inside a subprocess whose error the digest will paraphrase.
  for need in go git; do
    command -v "$need" >/dev/null 2>&1 || {
      echo "nightly: '$need' is not on PATH ($PATH)" >&2
      echo "nightly: reinstall with 'nightly.sh install' to capture the current PATH" >&2
      exit 127
    }
  done

  mkdir -p "$DIGEST_DIR"
  stamp=$(date -u +%Y-%m-%dT%H%M%SZ)
  digest="$DIGEST_DIR/$stamp.md"

  # Gates first: a merged prerequisite should release its dependants before
  # the coordinator decides what is ready (ADR 0014).
  bd gate check >/dev/null 2>&1 || true

  {
    echo "# fleet run $stamp"
    echo
    rc=0
    timeout "$RUN_CEILING" go run ./tools/fleet run -roster "$ROSTER" -execute 2>&1 || rc=$?
    # `|| echo "(run exceeded ...)"` used to sit here, which reported EVERY
    # non-zero exit as a timeout. The first unattended night died instantly on
    # "failed to run command 'go': No such file or directory" and the digest
    # recorded a 90-minute run that never happened. Only 124 is a timeout;
    # saying so is the difference between a diagnosis and a fiction.
    case "$rc" in
    0) ;;
    124) echo; echo "(run exceeded $RUN_CEILING and was stopped — this IS a timeout)" ;;
    126 | 127) echo; echo "(could not start, exit $rc: a command was missing or not executable." \
      "A systemd timer does not get your shell's PATH — reinstall with 'nightly.sh install')" ;;
    *) echo; echo "(run failed with exit $rc — NOT a timeout; read the log above)" ;;
    esac
  } | tee "$digest"

  echo
  echo "digest: $digest"

  # The account states when its quota returns, and fleet run records that in
  # .flywheel/quota-hold. Come back then, by itself. A run that ends on a rate
  # limit and waits for someone to notice is an outage with extra steps, and
  # the whole reason to schedule this in the first place was to not need a
  # person at 2am.
  #
  # This can chain: a resumed run that hits the wall again schedules another.
  # That is the intended behaviour and it is bounded three ways — each link
  # waits hours for a real reset, the review-weight budget refuses to dispatch
  # while PRs sit unreviewed (ADR 0009), and guard.sh stops everything.
  # To break it by hand: systemctl --user stop 'flywheel-resume-*'.
  # Every repo in the roster, not just this one. noteRateLimit writes the hold
  # into the repo whose builder hit the wall, so a Spotify builder's 429 lands
  # in Spotify's .flywheel/quota-hold — and reading only our own found a
  # two-day-old expired hold, computed a negative delay, and scheduled nothing.
  # The fleet then sat until a person noticed, which is the outage the hold
  # exists to prevent, reached by a different route.
  #
  # Earliest FUTURE hold wins: the account is one pool, so the first reset is
  # when work can start again wherever it was blocked.
  until_ts=$(go run ./tools/fleet holds -roster "$ROSTER" 2>/dev/null | head -1)
  if [ -n "$until_ts" ] && command -v systemd-run >/dev/null 2>&1; then
    if secs=$(( $(date -u -d "$until_ts" +%s 2>/dev/null || echo 0) - $(date -u +%s) )) \
       && [ "$secs" -gt 0 ]; then
      # +60s: waking exactly on the boundary races the reset and buys another
      # 429, which would schedule another wakeup for the same instant.
      # --setenv=PATH is the whole point of this line. systemd-run builds a
      # TRANSIENT unit, which inherits nothing from the installed
      # flywheel-nightly.service — so the PATH captured at install time does
      # not reach the resume, and the first one ever scheduled died with
      # "'go' is not on PATH" at exit 127. The timer was fixed and the thing
      # the timer schedules was not.
      systemd-run --user --on-active="$((secs + 60))s" \
        --setenv="PATH=$PATH" \
        --working-directory="$PWD" \
        --unit="flywheel-resume-$(date -u +%Y%m%dT%H%M%SZ)" \
        "$PWD/tools/flywheel/nightly.sh" run >/dev/null 2>&1 &&
        echo "quota hold until $until_ts — resuming automatically in $(( (secs + 60) / 60 ))m"
    fi
  fi
  ;;

status)
  systemctl --user list-timers flywheel-nightly.timer --no-pager 2>/dev/null | sed -n 1,2p || echo "timer not installed"
  if [ -f .flywheel/quota-hold ]; then
    echo; echo "QUOTA HOLD until $(head -1 .flywheel/quota-hold) — $(tail -1 .flywheel/quota-hold)"
    systemctl --user list-timers 'flywheel-resume-*' --no-pager 2>/dev/null | sed -n 2p
  fi
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
