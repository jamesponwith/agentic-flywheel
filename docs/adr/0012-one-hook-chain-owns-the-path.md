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

**lefthook owns `.git/hooks`; beads is invoked as a declared step.**

The first attempt gave the path to a neutral `.githooks/` directory. That
failed within hours, and instructively: beads *follows* `core.hooksPath` and
installs its own hooks wherever it points, so it renamed the chain script to
`pre-commit.old` and installed no replacement — leaving no pre-commit hook at
all. A neutral directory is not neutral if one tool writes to whatever the
setting names.

So ownership is inverted rather than shared. `core.hooksPath` stays unset,
`lefthook install` writes `.git/hooks`, and `lefthook.yml` declares a `beads`
command that calls `.beads/hooks/pre-commit`. One owner, one path, the other
tool called explicitly.

`tools/flywheel/install-hooks.sh` runs `lefthook install` once per clone.

A missing lefthook **warns loudly** rather than passing silently. A gate that
says nothing when it is absent is how this repo shipped a decorative one.

## Consequences

The Build gate is real — verified by committing deliberately unformatted code
and watching it be rejected, then committing clean code and watching it pass.
It runs in ~0.6s, which happens to match the claim that was previously untrue.

This was caught by the AI review panel on its first genuine run — it reported
"hooksPath points at .githooks but no pre-commit is committed there; Build gate
still never runs", which was exactly right. The stage that had been decorative
all project found the bug in its own repair.

Harder: adding a third hook system means declaring it in lefthook.yml. That is the cost of the two systems being unable to
coexist on one path, and it is cheaper than the alternative — which was a gate
nobody could tell was dead.
