#!/usr/bin/env bash
# Refuse the pushes ADR 0003 forbids an unattended agent — from the agent, and
# never from the human at the terminal.
#
# The enforcement used to be a GitHub ruleset. On a solo repo a ruleset that
# requires a pull request also blocks the maintainer from merging their own,
# so it was deleted, and deleting it left ADR 0003 as prose that nothing reads.
# Scoping the rule to agents gets the guarantee back without the deadlock: the
# builder cannot reach main, and the maintainer is never asked to satisfy a
# review requirement they are the only reviewer for.
#
# Reads git's pre-push stdin: <local-ref> <local-sha> <remote-ref> <remote-sha>.
set -euo pipefail

# Two independent signals, because either alone is escapable by accident.
# FLYWHEEL_AGENT is set by run.go for every builder it spawns; a linked
# worktree is structural, and survives an environment that got stripped
# somewhere between the spawn and the push.
agent="${FLYWHEEL_AGENT:-}"
# Resolve both before comparing. git answers --git-dir absolutely and
# --git-common-dir relatively when the caller is in a subdirectory, so a
# string compare called the main repo a worktree and refused the maintainer
# any push made from anywhere but the root — the deadlock this replaced.
linked=""
gd=$(cd "$(git rev-parse --git-dir)" 2>/dev/null && pwd -P) || gd=""
gc=$(cd "$(git rev-parse --git-common-dir)" 2>/dev/null && pwd -P) || gc=""
if [ -n "$gd" ] && [ -n "$gc" ] && [ "$gd" != "$gc" ]; then
  linked="worktree"
fi
if [ -z "$agent" ] && [ -z "$linked" ]; then
  exit 0 # a human at the terminal; ADR 0003 is about unattended agents
fi
who="${agent:-a builder worktree}"

# Deliberately a fixed list, not "the current branch": a builder pushing to
# main from a bead worktree is exactly the case this exists to stop, so what
# counts as protected must not be derived from where the pusher happens to be.
protected="main master"

rc=0
refused=""
while read -r _local_ref local_sha remote_ref _remote_sha; do
  [ -n "${remote_ref:-}" ] || continue
  # An all-zero local sha is a delete. Deleting main is worse than writing it.
  case "$remote_ref" in
  refs/tags/*)
    echo "push-guard: $who may not push tags — releases are not an agent's to cut (ADR 0003)." >&2
    rc=1
    refused="${refused:+$refused,}${remote_ref#refs/}"
    ;;
  refs/heads/*)
    branch="${remote_ref#refs/heads/}"
    for p in $protected; do
      if [ "$branch" = "$p" ]; then
        echo "push-guard: $who may not push to '$branch'." >&2
        echo "  Open a pull request from this branch instead; merging is the maintainer's (ADR 0003)." >&2
        case "$local_sha" in
        0000000*) echo "  (this push would DELETE '$branch')" >&2 ;;
        esac
        rc=1
        refused="${refused:+$refused,}$branch"
      fi
    done
    ;;
  esac
done

if [ "$rc" -ne 0 ]; then
  # Refusals are evidence. A guard that stops something silently cannot be
  # shown to have ever stopped anything.
  # Resolve guard.sh from the repository, not from $0. lefthook invokes this
  # as .lefthook/pre-push/push-guard.sh, so dirname was .lefthook/pre-push,
  # which holds no guard.sh — and that is the ONLY path on which the guard
  # ever runs for real. Every push.refused record in the log came from the
  # test suite calling the script directly; not one came from a refusal.
  # A symlink does not help: bash sets $0 to the path as invoked (fw-51q).
  root=$(git rev-parse --show-toplevel 2>/dev/null) || root=""
  guard="$root/tools/flywheel/guard.sh"
  [ -x "$guard" ] || guard="$(dirname "$0")/guard.sh"
  # 'agent' is a reserved key: guard.sh resolves who is acting from the
  # environment and refuses a caller-supplied duplicate with exit 2.
  #
  # Not silenced. The refusal has already happened and the push is already
  # refused, so a failure to record must not change the outcome — but it must
  # not be invisible either, because silence is exactly how this went unnoticed.
  if ! "$guard" log push.refused refs="$refused" 2>/dev/null; then
    echo "push-guard: refused, but could not record it via $guard" >&2
  fi
fi
exit "$rc"
