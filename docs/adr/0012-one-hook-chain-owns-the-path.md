# 0012. One chain owns core.hooksPath

Date: 2026-08-16
Status: accepted

## Context

`lefthook.yml` existed in all five repos and described the Build stage's inner
loop — gofmt, vet, short tests. It had never run. lefthook was installed
nowhere, no repo had a lefthook-generated `.git/hooks/pre-commit`, and three
repos set `core.hooksPath` to `.beads/hooks`, which silently overrides whatever
lefthook installs. Two hook systems both assumed ownership of one path and
beads won without saying so.

The published claim — "pre-commit runs gofmt, vet, and short tests in about a
second" — was therefore false for the project's entire life, and looked correct
the whole time because the config file was correct.

## Decision

Neither tool owns the path. `.githooks/` does, and it calls both: beads' hook
first, then lefthook. `tools/flywheel/install-hooks.sh` sets `core.hooksPath`
and installs lefthook, and is run once per clone.

A missing lefthook **warns loudly** rather than passing silently. A gate that
says nothing when it is absent is how this repo shipped a decorative one.

## Consequences

The Build gate is real — verified by committing deliberately unformatted code
and watching it be rejected, then committing clean code and watching it pass.
It runs in ~0.6s, which happens to match the claim that was previously untrue.

Harder: the chain is one more file to keep working, and adding a third hook
system means editing it. That is the cost of the two systems being unable to
coexist on one path, and it is cheaper than the alternative — which was a gate
nobody could tell was dead.
