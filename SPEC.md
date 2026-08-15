# Personal Agentic Flywheel — Spec

**One-liner:** The Intent → Build → Validate → Release → Learn flywheel from Capital One, rebuilt
lean for personal projects: a template repo + conventions where AI agents do the work and every
stage feeds the next.

**Why this project:** The flywheel already exists on the resume as prose. This makes it inspectable:
a public, working instance of agent-driven development with real quality gates and real feedback
loops. It also compounds — every future personal project (starting with the LLM resiliency router)
gets built inside it, so the flywheel produces evidence continuously.

## The five stages, personal edition

Mapping from the C1 version. (The Build stage at C1 that you were blanking on: it's the **inner
loop** — the agent/dev turning intent artifacts into code under fixed conventions, with fast local
feedback: coding standards, scaffolds, pre-commit checks, unit tests run on save. Testing *suites*
belong to Validate; the tests an agent runs every 30 seconds while iterating belong to Build.)

| Stage    | Capital One                                        | Personal                                                    |
|----------|----------------------------------------------------|-------------------------------------------------------------|
| Intent   | ADRs, specs, beads                                 | Same, smaller: SPEC.md, ADRs, beads (`bd`)                   |
| Build    | Agent inner loop, standards, fast local tests      | Claude Code + skills (ponytail), CLAUDE.md conventions, worktrees, pre-commit |
| Validate | Unit/component/live-dep suites, PR pipeline gating dev images, AI review (ponytail), tenant validation | Unit/component/live-dep, GitHub Actions PR gate, local pre-push AI review, pilot-project canary |
| Release  | Official release image to all tenants              | Tagged semver release via goreleaser; deploy to own hosts    |
| Learn    | DORA reports + signals feeding back (in dev)       | DORA-lite collector from GitHub API → monthly retro → new beads issues |

## Deliverables

1. **`flywheel-template`** — a GitHub template repo containing the entire loop, cloneable for any
   new Go project. This is the personal analog of the C1 Admin ecosystem repo: one clone onboards
   a new project (or a new agent) into the full workflow — conventions, hooks, gates, and release
   machinery — with zero per-project setup. Name that parallel in the writeup.
2. **A running instance** — the LLM resiliency router built through it end-to-end (pilot tenant).
3. **A writeup** — "An agentic development flywheel for one" — the C1 pattern, what transfers to a
   team of one + agents, what doesn't. This doc is the DeepMind-visible artifact.

## Template repo contents

```
flywheel-template/
├── CLAUDE.md                  # conventions: bd for tracking, ponytail active, test-first,
│                              # stdlib-first, commit protocol
├── SPEC.md                    # template with required sections
├── docs/adr/
│   ├── template.md            # context / decision / consequences, ~20 lines max
│   └── 0001-record-adrs.md
├── .claude/settings.json      # permissions, hooks (fmt+vet on stop, bd prime on start)
├── .github/workflows/
│   ├── pr.yml                 # Validate: lint → unit → component
│   ├── release.yml            # Release: tag → goreleaser → GitHub Release
│   └── learn.yml              # Learn: weekly cron → DORA-lite collector → dashboard commit
├── .golangci.yml
├── lefthook.yml               # pre-commit: gofmt, go vet, go test ./... -short
└── tools/dora/                # the one piece of custom code in this project (see below)
```

### Stage details

**Intent.** Nothing new to build — codify usage:
- Every feature starts as a `bd` issue; anything > 1 session gets a SPEC.md section first.
- ADRs for decisions that would take > 5 minutes to re-derive (why stdlib-only, why priority
  routing before latency routing). ADRs are ~20 lines; if it's longer it's a spec.
- CLAUDE.md tells agents where intent lives: read SPEC.md + open beads issue before writing code.

**Build.** Also mostly codification:
- Claude Code with ponytail active (already in use — the C1 parallel writes itself).
- Conventions in CLAUDE.md: table-driven tests, no new deps without an ADR, `ponytail:` comments
  for deliberate shortcuts.
- lefthook pre-commit keeps the inner loop honest: fmt, vet, short tests. Fast (< 10s) or it gets
  skipped — speed is a feature of Build, not a nicety.
- Parallel agent work uses git worktrees (Claude Code's worktree isolation), mirroring C1's
  multi-agent orchestration at personal scale.

**Validate.** The PR pipeline is the gate — no green, no merge:
- `pr.yml`: golangci-lint → `go test ./...` (unit) → component suite (docker compose up fake deps
  — the Localstack analog). **AI review** runs locally instead: a lefthook pre-push hook invoking
  Claude with the ponytail-review skill, advisory-only (template ADR 0002 — no CI API keys, no
  per-PR token spend, review lands before the PR even exists). Human (you) is the final approver;
  the AI review is a gate assistant, not an auto-merge authority.
- Live-dep suite (`-tags=live`) runs on a schedule, not per-PR (costs real API pennies).
- "Dev image to tenant" analog: merged main auto-deploys the router to its host in **Learn mode**;
  it must run clean for a period before a release tag promotes the build. That's the personal
  version of gating the base image in a tenant app before official release.

**Release.**
- Semver tags → `release.yml` → goreleaser → binaries + changelog on GitHub Releases.
- Deploy = pull new binary on the host (systemd unit; a `deploy.sh` is fine — no ArgoCD for one
  machine).

**Learn.** The only real code in this project (~300 lines of Go, `tools/dora/`):
- Collector pulls from GitHub API per repo: deploy frequency (releases), lead time (first commit →
  release containing it), change-failure rate (`incident`-labeled issues / releases + reverts),
  MTTR (incident open → close).
- Emits `dora.json` + a small trend page — served on jamesponwith.github.io, terminal-styled.
  Public DORA metrics for personal projects: unusual, memorable, and it closes the loop in public.
- Monthly ritual (calendar reminder, not automation): read the trends, file `bd` issues for what
  they suggest. Insight → Intent. The flywheel turns.

## Milestones

- **M1** (an evening): template repo with CLAUDE.md, ADR/spec templates, lefthook, pr.yml
  (lint + unit only). Router project adopts it immediately.
- **M2** ✅: component suite in CI. (Landed as in-process `httptest` fakes — the router's chaos
  harness — running inside the existing unit job; no docker-compose stage needed until a real
  external dep appears. AI review: done in M1 as a local pre-push hook, ADR 0002.)
- **M3** ✅: release.yml + goreleaser in the template; router shipped v0.1.0 through it
  (4 platform binaries + changelog, 59s pipeline) with a deploy.sh verified against the
  real release. Host deploy (linux box, systemd) waits on the box being set up.
- **M4** (mostly done): DORA-lite collector + learn.yml + dora.html shipped in template and
  router (first real snapshot committed by the workflow 2026-08-08); WRITEUP.md and the router
  blog post drafted, pending voice pass. Dashboard live at jamesponwith.github.io/dora.html
  (fetches each repo's dora.json from raw.githubusercontent). Python variant shipped:
  github.com/jamesponwith/flywheel-template-py (uv/ruff/pytest gate proven by smoke PR;
  learn.yml reuses the Go collector via `go run …/tools/dora@latest`). Remaining: publish
  the blog post after the voice pass; bootstrap brax-tennis-rl from the Python template.

## Success criteria

- The router is built entirely inside the flywheel — every merged PR passed the full Validate gate.
- One month of real DORA data on a public dashboard.
- ✅ Starting project #3 (brax-tennis-rl) from the template takes < 30 minutes.
  (Measured 2026-08-11: **2m55s** from `gh repo create --template flywheel-template-py` to
  smoke PR merged through a green gate — real SPEC in place, uv/ruff/pytest hooks live,
  beads filed, first DORA snapshot committed by learn.yml minutes later.)

---

# Chapter 2 — v2 scope (opened 2026-08-15)

V1 answered "does the C1 flywheel survive at a team of one?" — yes, five stages, three instances,
one released binary. V2 answers the two questions v1 left open:

1. **The cycle stops too early.** The flywheel ends at "binary on a host". Nothing observes the
   running thing, which means change-failure rate and MTTR — half of DORA — are fed by remembering
   to label a GitHub issue `incident`. By this spec's own rule, that is a process that will quietly
   stop happening.
2. **The agent is barely agentic.** One agent, one session, a human sitting in front of it. Worktree
   isolation, subagents, scheduled runs, and beads' own multi-agent primitives (`swarm`, `gate`,
   `merge-slot`) are all installed and unused.

Tracked as six epics in this repo's `bd` board (`bd list`, prefix `fw`).

## The sixth stage

| Stage    | Personal (v1)                                             | v2 change                                        |
|----------|-----------------------------------------------------------|--------------------------------------------------|
| Intent   | SPEC.md, ADRs, beads                                       | spec → bead-graph decomposition; external signals file beads |
| Build    | Claude Code + ponytail, CLAUDE.md, lefthook                | unattended runs: one ready bead → worktree → PR  |
| Validate | pr.yml gate, local pre-push AI review                      | three-lens review panel, findings ledger, supply chain |
| Release  | goreleaser, semver tags, deploy.sh                         | SBOM + signature + provenance; post-deploy smoke |
| **Operate** | *(absent — the artifact ran unobserved)*                | **SLOs, probes, auto-filed incidents, auto-rollback** |
| Learn    | DORA-lite from the GitHub API                              | agent-effectiveness + cost metrics; automated retro |

Operate is the honest name for the gap between "released" and "learned". At C1 that gap was filled
by tenants running the artifact and someone else owning its telemetry. Solo, nobody is watching
unless something watches — and Learn cannot be truthful until something does.

## Epics

| Epic     | Title                                                        | Why it exists                                                  |
|----------|--------------------------------------------------------------|----------------------------------------------------------------|
| `fw-wma` | Operate: promote the Release→Learn gap to a first-class stage | Makes CFR and MTTR measured rather than remembered              |
| `fw-lb8` | Autonomous loop                                              | The queue drains overnight; the human still merges              |
| `fw-l8k` | Validate v2: review panel, findings ledger, supply chain      | Diversity of review, and a record of whether review works       |
| `fw-e7e` | Learn v2: measure the agent, not just the code                | Nobody has public numbers on what agentic development costs     |
| `fw-cpd` | Intent v2: self-decomposing specs, signals as beads           | The stage with the most hand-work and the least machinery       |
| `fw-fsa` | Distribution: five repos on one flywheel, and the story       | A template repo solves onboarding once and then rots            |
| `fw-wb2` | Inter-agent protocol: identity, territory, claims, handoffs   | Two agents in one repo without a contract is a race, not a fleet |
| `fw-oef` | Agent fleet: many agents, five repos, one budget              | Five repos are no longer tractable by hand                      |

## v2 success criteria

- An SLO breach on a deployed instance files an incident, its recovery closes it, and the router's
  `dora.json` shows a non-zero CFR and MTTR that no human hand-authored.
- Seven consecutive unattended nights producing green, reviewable PRs — with a budget cap, an audit
  log, and a kill switch verified by tripping it mid-run.
- One dashboard covering every flywheel repo, carrying agent-effectiveness metrics alongside DORA,
  with a month of real history.
- `flywheel doctor` brings an existing instance up to current template in one command.
- Two agents work one repo concurrently without corrupting each other — territory reserved before
  edit, claims leased and reclaimable, handoffs acknowledged — proven by a conformance harness that
  kills agents mid-flight, not by it having gone fine once.
- One coordinator distributes ready work across all five repos overnight inside a declared budget,
  with a live view of who holds what and a kill switch verified by tripping it with agents active.

## Resolved: scope is a budget (ADR 0001)

`fw-2bv` is closed. The v1 non-goal banning fleets and inter-agent protocols named a category but
justified a cost; it is replaced by an explicit budget (see Non-goals below and `docs/adr/0001`).
Fleets and protocols are in scope, priced rather than prohibited, with review capacity — not
compute — named as the binding constraint.

## The agent stack

Fleets need identity, file-level mutual exclusion, durable acknowledged messaging, and a
tamper-evident journal. All four already run on this machine, so the protocol epic is wiring and
convention rather than new services — which is the only reason it fits the budget (ADR 0004).

| Layer | Owns | Provided by |
|-------|------|-------------|
| Work graph | issues, dependencies, claims, gates, merge-slot, swarms | `bd` (beads) |
| Coordination | agent identity, file reservations with fencing tokens, conversations, acks, event journal | `blackbird` (systemd daemon, MCP-connected) |
| Execution | worktrees, subagents, skills, scheduled runs | Claude Code |

`bd` claims the **work**; blackbird reserves the **files**. Both are required before an agent edits
anything. Agents fail closed: no reservation, no edit.

## v2 ADRs

| ADR | Decision |
|-----|----------|
| 0001 | Scope is a budget, not a ban — fleets and protocols priced, not prohibited |
| 0002 | Operate is a first-class stage — incidents filed and closed by machine |
| 0003 | Autonomy boundaries — unattended agents branch and PR; never merge, tag, deploy, or read secrets |
| 0004 | Agents stand on beads + blackbird + Claude Code — build none of it again |

## Non-goals

- ~~Multi-repo orchestration dashboards, agent fleets, inter-agent protocols~~ — **superseded
  2026-08-15 by ADR 0001.** The non-goal named a category but justified a cost, and at five repos
  the same reasoning that once argued against orchestration now argues for it. Replaced by a budget:
  every stage and epic declares a standing cost in minutes and dollars per month, and anything over
  budget for two consecutive retros gets deleted or automated. Fleets and inter-agent protocols are
  priced, not prohibited.
- Anything that outruns one human's review capacity. Review mornings, not compute, are the binding
  constraint on the fleet; a fleet that produces more plausible PRs than you can read properly has
  failed its budget no matter what it cost to run.
- Any stage that requires discipline you won't sustain. If a gate gets bypassed twice, delete it or
  automate it — a documented-but-ignored process is worse than none (put that line in the writeup).
