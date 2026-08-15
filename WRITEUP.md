# An agentic development flywheel for one

At Capital One I worked inside a development flywheel: intent artifacts fed
an agent-assisted build loop, a PR pipeline gated everything, releases
promoted only what had proven itself, and DORA metrics fed back into what we
built next. It ran across hundreds of engineers and a platform team.

The question that followed me out the door: how much of that survives when
the team is one person and a coding agent?

Almost all of it. The catch is ruthlessness about what each stage costs to
keep running. This is the working system:
[flywheel-template](https://github.com/jamesponwith/flywheel-template) (Go)
and [flywheel-template-py](https://github.com/jamesponwith/flywheel-template-py)
(Python), template repos carrying all five stages, plus two pilot projects
built entirely inside them — an
[LLM resiliency router](https://github.com/jamesponwith/llm-resiliency-router)
and a [reinforcement-learning tennis agent](https://github.com/jamesponwith/brax-tennis-rl).

## The five stages, translated

**Intent.** Same artifacts, smaller. A SPEC.md per project. ADRs capped at
20 lines. Issue tracking in [beads](https://github.com/steveyegge/beads)
with one rule: no bead, no build. Intent is a file the agent loads, not a
meeting.

**Build.** Claude Code under a standing YAGNI discipline: stdlib first,
shortest working diff, deliberate shortcuts marked in comments. Pre-commit
runs fmt, vet, and short tests in about a second. Speed is a feature — when
the tennis project's test suite grew a two-minute physics compile, the gate
got a fast lane the same day, because a slow gate is a skipped gate.

**Validate.** The PR pipeline: lint plus tests, green in ~30 seconds, no
green no merge. The one deliberate departure from the C1 shape: AI code
review runs *locally, in a pre-push git hook* — no CI minutes, no API-key
secrets, and findings land before the PR exists. It's advisory; I'm the
merge authority. It earns its keep: across the pilots it caught duplicated
types, dead config knobs, and a config bug that would have wasted an
hour-long training run.

**Release.** Semver tag → goreleaser → binaries and changelog, 59 seconds
end to end. A deploy script pulls the release asset and restarts a systemd
unit. Verified against a real release the day it shipped.

**Learn.** ~150 lines of Go pull deploy frequency, lead time, change-failure
rate, and MTTR from the GitHub API. A weekly Action commits the snapshot
back to each repo — the file's git history *is* the trend line — and a
[public dashboard](https://jamesponwith.github.io/dora.html) renders all of
it. Public DORA metrics for personal projects: unusual, and it closes the
loop in the open.

## What the pilots proved

The router went from template to released v0.1.0 — priority failover,
quality canaries, learn/action modes, hedging — through eleven gated PRs.
The tennis agent went from spec to three completed phases (95.2%
interception, 71.7% returns, self-play rallies that lengthen across
generations) through twenty-three more. Bootstrapping the tennis project
from the Python template took **2 minutes 55 seconds** against a 30-minute
budget. Every resilience and RL claim in both repos has a recorded demo or
a curve behind it.

## What transferred, what didn't

Transferred: intent-as-files, the fast inner loop, hard PR gates,
observe-before-act rollout (the router ships in learn mode and earns the
right to act — the C1 rollout story in miniature), DORA as the feedback
signal.

Adapted: AI review moved from the pipeline to the laptop. The component
suite runs in-process against a chaos harness instead of docker-compose.

Dropped: orchestration dashboards, agent fleets, inter-agent protocols. The
personal version must stay cheap enough to actually use. Standing rule: if
a gate gets bypassed twice, delete it or automate it — a
documented-but-ignored process is worse than none.

## The part that surprised me

The stages audit each other. CI caught a test race the local suite never
hit. The AI reviewer flagged code the linter loved. A demo recording exposed
a rendering bug no test would ever find. One person plus an agent doesn't
get a team's diversity of eyes — but a well-built loop manufactures a
surprising amount of it.
