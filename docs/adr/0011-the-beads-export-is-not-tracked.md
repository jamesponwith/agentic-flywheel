# 0011. The beads export is not tracked in git

Date: 2026-08-16
Status: accepted

## Context

`.beads/issues.jsonl` was committed, and any two branches that both touched the
board conflicted on it. That happened three times in a single session, and the
conflict is always meaningless: the file is derived, so the resolution is
"regenerate", never "merge the text". It also blocks branch switching whenever
the export is dirty, which bit twice more.

beads has said so all along — the tool prints "issues.jsonl is an export, not
cross-machine sync or source of truth" on every checkout. The reason to keep
tracking it was that nothing else was durable: a Dolt remote was configured but
had never been pushed, so `refs/dolt/data` did not exist and the JSONL really
was the only copy off this machine.

## Decision

Push the database (`bd dolt push`, now live at `refs/dolt/data` on origin) and
stop tracking the exports. `bd export` remains available for inspection and
one-off interchange; it is no longer a committed artifact.

`fleet hydrate` keeps its JSONL restore path, because a freshly cloned instance
whose repo predates this still has only the committed file — and hydrate must
work on repos as they are, not as they should be.

## Consequences

The recurring conflict class disappears, and the board stops being a merge
hazard for every parallel agent — which matters more as concurrency rises.
Harder: the durable copy now lives in a ref most tooling does not display, so
`bd dolt push` becomes a step that can be forgotten silently. That wants a
check; until there is one, this trades a visible nuisance for an invisible one.
