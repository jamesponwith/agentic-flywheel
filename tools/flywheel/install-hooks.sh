#!/usr/bin/env bash
# Wire the hook chain and install what it needs (ADR 0012).
#
# Run once per clone. `fleet doctor` reports repos where it has not been run.
set -euo pipefail
root=$(git rev-parse --show-toplevel)
cd "$root"

git config core.hooksPath .githooks
echo "core.hooksPath -> .githooks"

if command -v lefthook >/dev/null; then
  echo "lefthook: already installed ($(command -v lefthook))"
else
  echo "lefthook: installing…"
  go install github.com/evilmartians/lefthook@latest
  echo "lefthook: installed to $(go env GOPATH)/bin — ensure that is on PATH"
fi

# Prove it, rather than trusting it. A gate you have not watched fail is a gate
# you have not got.
echo "verifying the gate actually rejects bad input…"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
printf 'package main\nfunc  Bad( ){}\n' > "$tmp/bad.go"
if gofmt -l "$tmp" | grep -q bad.go; then
  echo "  gofmt detects unformatted Go: OK"
else
  echo "  WARNING: gofmt did not flag deliberately bad input" >&2
fi
echo "done — commit something unformatted to confirm the hook rejects it"
