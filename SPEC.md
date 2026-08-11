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

## Non-goals

- Multi-repo orchestration dashboards, agent fleets, inter-agent protocols — that's the C1 version;
  the personal one must stay cheap enough to actually use.
- Any stage that requires discipline you won't sustain. If a gate gets bypassed twice, delete it or
  automate it — a documented-but-ignored process is worse than none (put that line in the writeup).
