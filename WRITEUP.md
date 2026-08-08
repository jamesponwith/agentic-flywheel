# An agentic development flywheel for one

> **DRAFT** — structure and evidence are in place; voice pass pending.

At Capital One I worked inside a development flywheel: intent artifacts fed an
agent-assisted build loop, a PR pipeline gated everything, releases promoted
only what had proven itself, and DORA metrics fed back into what we built
next. It ran across hundreds of engineers and a platform team. The obvious
question when I left: how much of that survives when the team is one person
and a coding agent?

The answer, it turns out, is almost all of it — if you're ruthless about what
each stage costs to keep running. This is the working system:
[flywheel-template](https://github.com/jamesponwith/flywheel-template), a
GitHub template repo carrying all five stages, and
[llm-resiliency-router](https://github.com/jamesponwith/llm-resiliency-router),
the pilot project built entirely inside it.

## The five stages, translated

**Intent.** Same artifacts, smaller: a SPEC.md per project, ADRs capped at
~20 lines, and [beads](https://github.com/steveyegge/beads) for issue
tracking with one rule — *no bead, no build*. The agent reads the spec and
the open bead before writing code; intent is a file the agent loads, not a
meeting.

**Build.** The inner loop is Claude Code under a standing YAGNI discipline
(the "ponytail" skill: stdlib first, shortest working diff, deliberate
shortcuts marked in comments). lefthook pre-commit runs gofmt, vet, and
short tests in about a second — fast enough that neither human nor agent is
tempted to skip it. Session hooks re-prime the agent with open issues on
start and block it from stopping with unformatted code.

**Validate.** The PR pipeline is the gate: golangci-lint plus the full test
suite, green in ~30s per PR, no green no merge. The one deliberate departure
from the C1 shape: **AI code review runs locally, in a pre-push git hook**,
not in CI (template ADR 0002). A nested `claude -p` review of
`origin/main...HEAD` costs no CI minutes, needs no API-key secrets in
GitHub, and lands before the PR even exists. It is advisory — the human is
the merge authority — and it earns its keep: across the router's eleven PRs
it caught a duplicated type, two stdlib simplifications, a dead config knob,
and an over-general concurrency cleanup.

**Release.** Semver tag → goreleaser → static binaries for four platforms
plus changelog, 59 seconds end to end. A `deploy.sh` pulls the release asset
for the current host and restarts a systemd unit; it was verified against
the real v0.1.0 release the day it shipped.

**Learn.** `tools/dora` (~150 lines of stdlib Go) pulls deploy frequency,
lead time, change-failure rate, and MTTR from the GitHub API. A weekly
Action commits `docs/dora.json` back to the repo — the file's git history
*is* the trend line — and a static page renders the latest snapshot. The
monthly ritual is reading it and filing beads for what it suggests. Insight
→ Intent. The flywheel turns.

## What the pilot proved

The router went from `gh repo create --template` to a released v0.1.0 —
priority failover, quality canaries, learn/action modes, hedging, telemetry
— in eleven PRs, every one through the full gate. Bootstrap from template
took ~3 minutes against a 30-minute budget. Three terminal-recorded demos
(failover, canary ejection, hedging) are in the router README as proof the
resilience claims run, not just compile.

## What transferred, what didn't

Transferred cleanly: intent-as-files, the fast inner loop, hard PR gates,
observe-before-act rollout (the router's learn mode is the C1 engine's
rollout story in miniature), DORA as the feedback signal.

Adapted: AI review moved from the pipeline to the laptop (cost, secrets,
and it's *earlier* there); the component suite runs in-process with a chaos
harness instead of docker-compose, so it lives inside the ordinary unit job.

Dropped: orchestration dashboards, agent fleets, inter-agent protocols.
The personal version has to stay cheap enough to actually use — a
documented-but-ignored process is worse than none. The governing rule: if a
gate gets bypassed twice, delete it or automate it.

## The part that surprised me

The flywheel's stages audit each other. CI caught a test race the local
suite never hit. The AI reviewer flagged code the linter loved. The canary
demo exposed an em-dash the recorder's font couldn't draw. One person plus
an agent doesn't get a team's diversity of eyes for free — but a well-built
loop manufactures a surprising amount of it.
