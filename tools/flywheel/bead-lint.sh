#!/usr/bin/env bash
# No bead, no build — checked rather than remembered.
#
# Verifies a branch references a real bead. Advisory by default: run with
# --strict to make it fail. Per ADR 0001's standing rule, this stays advisory
# for a fortnight and only then blocks — and if it turns out to be annoying
# enough to bypass twice, it gets deleted rather than argued with.
set -euo pipefail

STRICT=0
[ "${1:-}" = "--strict" ] && STRICT=1

BRANCH="${GITHUB_HEAD_REF:-$(git rev-parse --abbrev-ref HEAD)}"
RANGE="${BASE_REF:-origin/main}...HEAD"

# A bead id is <prefix>-<suffix>, optionally with a child segment: fw-oef.8
BEAD_RE='[a-z][a-z0-9-]*-[a-z0-9]+(\.[0-9]+)?'

found=""
if [[ "$BRANCH" =~ ^bead/($BEAD_RE)$ ]]; then
  found="${BASH_REMATCH[1]}"
else
  # Fall back to commit subjects on the branch.
  found=$(git log --format=%s "$RANGE" 2>/dev/null \
    | grep -oE "^$BEAD_RE" | head -1 || true)
fi

if [ -z "$found" ]; then
  echo "no bead referenced: branch is '$BRANCH' and no commit subject starts with a bead id" >&2
  echo "  name the branch bead/<id>, or start a commit subject with '<id>: '" >&2
  [ "$STRICT" = 1 ] && exit 1
  echo "::warning::no bead referenced on this branch"
  exit 0
fi

if command -v bd >/dev/null 2>&1; then
  if ! bd show "$found" >/dev/null 2>&1; then
    echo "bead '$found' is referenced but does not exist" >&2
    [ "$STRICT" = 1 ] && exit 1
    echo "::warning::bead $found not found"
    exit 0
  fi
fi

echo "bead: $found"
