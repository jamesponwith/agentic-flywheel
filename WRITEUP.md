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
(Python), template repos carrying all six stages, plus two pilot projects
built entirely inside them — an
[LLM resiliency router](https://github.com/jamesponwith/llm-resiliency-router)
and a [reinforcement-learning tennis agent](https://github.com/jamesponwith/brax-tennis-rl).

## The six stages, translated

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

**Operate.** The stage I didn't know was missing until the metrics lied. An
SLO file, a prober, and a `/healthz` endpoint that reports its version. A
sustained breach files an incident; recovery closes it. Deploys smoke
themselves and roll back if the smoke fails.

At Capital One this stage was invisible to me because tenants ran the
artifact and someone else owned its telemetry. Solo, nobody is watching
unless something watches — and until something did, change-failure rate and
MTTR were measuring how reliably I remembered to label a GitHub issue.

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

Dropped, then un-dropped: orchestration, agent fleets, inter-agent
protocols. I ruled all three out at the start — that's the C1 version, and
the personal one has to stay cheap enough to actually use.

I was wrong, and the way I was wrong is the interesting part. The rule named
a *category* but justified a *cost*. At one project, "don't orchestrate" and
"stay cheap" pointed the same way. At five repos they point in opposite
directions: the hand-work of keeping five queues moving is now the expensive
option. So the ban became a budget — every stage declares what it costs in
minutes and dollars per month, and anything over budget for two consecutive
retros gets deleted. Fleets are priced, not prohibited.

The constraint that actually binds turned out not to be compute. It's review
capacity. Agents produce PRs faster than one person can read them properly,
and an unreviewed merge is worse than an empty queue — so the fleet is sized
to a review morning, not to a machine.

Standing rule underneath all of it: if a gate gets bypassed twice, delete it
or automate it. A documented-but-ignored process is worse than none.

## The part that surprised me

The stages audit each other. CI caught a test race the local suite never
hit. The AI reviewer flagged code the linter loved. A demo recording exposed
a rendering bug no test would ever find. The coordinator's very first run
across all five repos reported that it couldn't read one of them — which
became a bead, which the agent loop then fixed.

One person plus an agent doesn't get a team's diversity of eyes. A
well-built loop manufactures a surprising amount of it.

## What I still can't tell you

Whether any of this is *worth* it, in full. Some of it I can now answer,
because the dashboard grew a second panel pointed at the agents instead of
the code:

- **First-pass gate rate** — 100% on the router (13/13), 96% on the tennis
  agent (24/25). PRs that were green on their first CI run.
- **Rework** — median zero commits after a PR opened, across both.

Those are better than I expected, and I distrust them slightly for that
reason: a solo repo has no reviewer to send work back, so "no rework" partly
measures the absence of a second opinion rather than the presence of quality.

What I still can't tell you: what an agent-built feature costs in tokens,
and whether the AI reviewer catches more than it costs. The first needs cost
accounting the audit log doesn't do yet. The second needs a ledger of review
findings and what became of them — including the rejected ones, because a
record of only the good calls can't produce a false-positive rate.

Both are built or building. Nobody publishes these numbers, which is exactly
why I want them public — they're the numbers I'd want from anyone else
claiming agentic development works.
