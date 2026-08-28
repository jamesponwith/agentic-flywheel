#!/usr/bin/env bash
# outbox.sh — the handoff for files a builder may not write (ADR 0015).
#
# Builders cannot write under .claude/ — the harness refuses the path in
# unattended sessions, and the refusal is kept on purpose: those files are the
# agent's own leash. So the change travels as content instead of authority.
# The builder commits the full intended file at .flywheel/outbox/<path>, which
# maps to .claude/<path> — the prefix is implied, not spelled, because the
# harness refuses ANY path containing `.claude/`, a mirror of it included;
# that refusal is the reason this script exists. The PR carries the entry; on
# the PR branch the HUMAN runs `apply`, which moves each file into place and
# stages both sides, so the same PR shows the real diff before merge.
#
# ponytail: whole files only, targets under .claude/ only. A deletion under
# .claude/ is not expressible; the day one is needed, add a manifest — do not
# invent a convention of empty files.
set -euo pipefail
# Command substitutions do not inherit errexit by default, and every command
# here validates via $(checked_entries) — without this a refusal inside the
# substitution would vanish and the command would proceed on a partial list.
shopt -s inherit_errexit

REPO_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
OUTBOX="$REPO_DIR/.flywheel/outbox"
GUARD="$(cd "$(dirname "$0")" && pwd)/guard.sh"

usage() {
  cat <<'USAGE'
usage: outbox.sh <command>

An entry at .flywheel/outbox/<path> is the intended content of .claude/<path>.

  status   list pending entries and whether each is new or modifies a file
  diff     show what apply would change, as real diffs against the targets
  apply    move entries into place and stage them — the human's step, on the
           PR branch, from a checkout that is not a builder worktree
USAGE
}

# entries — paths relative to the outbox root (and therefore to .claude/), one
# per line. Refuses an outbox that holds anything but regular files: a symlink
# silently skipped would be a change that vanishes between review and apply.
entries() {
  [ -d "$OUTBOX" ] || return 0
  local odd
  odd=$(find "$OUTBOX" ! -type f ! -type d -print)
  if [ -n "$odd" ]; then
    echo "outbox.sh: refusing non-regular entries:" >&2
    printf '%s\n' "$odd" >&2
    return 2
  fi
  (cd "$OUTBOX" && find . -type f | sed 's|^\./||') | LC_ALL=C sort
}

# check_target <rel> — the target .claude/<rel> must still be under the repo's
# .claude/ after symlinks resolve. The mapping makes any other target
# inexpressible; a symlinked directory under .claude/ is the one way out, and
# it is refused here.
check_target() {
  local rel="$1" resolved
  resolved="$(realpath -m "$REPO_DIR/.claude/$rel")"
  case "$resolved" in
    "$REPO_DIR/.claude/"*) ;;
    *)
      echo "outbox.sh: refusing '$rel': resolves outside $REPO_DIR/.claude/" >&2
      return 2
      ;;
  esac
}

# A refusal inside <(entries) would be invisible — a process substitution's
# exit status never reaches the caller — so every command captures the list
# first, where `set -e` sees the failure, and validates every entry before
# acting on any. A refusal after the first mv would leave a half-applied
# outbox, which is exactly the ambiguity this handoff exists to remove.
checked_entries() {
  local list rel
  list=$(entries) || return 2
  while IFS= read -r rel; do
    [ -n "$rel" ] || continue
    check_target "$rel" || return 2
  done <<< "$list"
  printf '%s' "$list"
}

cmd_status() {
  local list rel any=0
  list=$(checked_entries)
  while IFS= read -r rel; do
    [ -n "$rel" ] || continue
    any=1
    if [ -e "$REPO_DIR/.claude/$rel" ]; then echo "modifies  .claude/$rel"; else echo "new       .claude/$rel"; fi
  done <<< "$list"
  [ "$any" = 1 ] || echo "outbox empty"
}

cmd_diff() {
  local list rel any=0
  list=$(checked_entries)
  while IFS= read -r rel; do
    [ -n "$rel" ] || continue
    any=1
    # --no-index exits 1 when the files differ; that is the expected case.
    git -C "$REPO_DIR" diff --no-index -- \
      "$([ -e "$REPO_DIR/.claude/$rel" ] && echo "$REPO_DIR/.claude/$rel" || echo /dev/null)" \
      "$OUTBOX/$rel" || true
  done <<< "$list"
  [ "$any" = 1 ] || echo "outbox empty"
}

cmd_apply() {
  # The refusal that encodes the decision: apply is the human's step. A builder
  # carries FLYWHEEL_AGENT or a spawner-written .flywheel/agent; a human
  # checkout has neither. Removable by a determined agent — defence in depth,
  # not a sandbox (ADR 0003 amendment); the bound remains the human merge.
  if [ -n "${FLYWHEEL_AGENT:-}" ] || [ -f "$REPO_DIR/.flywheel/agent" ]; then
    echo "outbox.sh: apply is the human's step — a builder does not write under .claude/ (ADR 0015)" >&2
    echo "outbox.sh: run it from your own checkout of the PR branch, not a builder worktree" >&2
    return 1
  fi
  local list rel n=0
  list=$(checked_entries)
  while IFS= read -r rel; do
    [ -n "$rel" ] || continue
    mkdir -p "$(dirname "$REPO_DIR/.claude/$rel")"
    mv "$OUTBOX/$rel" "$REPO_DIR/.claude/$rel"
    git -C "$REPO_DIR" add -A -- ".claude/$rel" ".flywheel/outbox/$rel"
    echo "applied   .claude/$rel"
    n=$((n + 1))
  done <<< "$list"
  if [ "$n" = 0 ]; then echo "outbox empty"; return 0; fi
  find "$OUTBOX" -type d -empty -delete
  "$GUARD" log outbox.applied "files=$n"
  echo "staged $n file(s); review with 'git diff --cached' and commit on this branch"
}

case "${1:-}" in
  status) cmd_status ;;
  diff) cmd_diff ;;
  apply) cmd_apply ;;
  *) usage; exit 2 ;;
esac
