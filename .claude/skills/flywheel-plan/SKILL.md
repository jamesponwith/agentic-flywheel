---
name: flywheel-plan
description: Turn a SPEC.md section into a reviewed graph of beads — an epic, its children, acceptance criteria, and the dependency edges between them. Use when starting a new body of work, decomposing a milestone, or when asked to "plan this" or "file beads for this". Always dry-runs first; the human approves the plan, not the beads afterwards.
---

# flywheel-plan

Intent is the stage with the most hand-work and the least machinery. Every bead
on every board so far was typed by a human. This makes decomposition cheap
without making it automatic — you propose a graph, the human edits the *plan*,
and only then does anything get created.

`bd` already accepts a dependency graph as JSON (`bd create --graph`). This
skill is the thing that writes one.

## 1. Read the intent, not just the section

The named SPEC.md section, plus: the ADRs governing that area, the existing
board (`bd list`) so you don't propose what already exists, and enough of the
code to know what is genuinely new versus already half-built.

If the section is too vague to decompose — no acceptance criteria you could
verify, no clear boundary — say so and propose the two or three questions that
would fix it. A plan built on a vague spec produces beads nobody can close.

## 2. Propose the graph

An epic, and children that are each **one session of work**. The test for a
good child bead: could an agent claim it and know when it was done?

Every bead needs:

- **A title that names the outcome**, not the activity. "Incidents close
  themselves on recovery" beats "work on incident handling".
- **A description that says why it exists** — the failure it prevents, the
  thing it unblocks. Six weeks later this is the only context anyone has.
- **Acceptance criteria you could verify** and, ideally, that a test could.
  "Works well" is not acceptance. "A simulated outage produces one incident
  with open and close timestamps" is.
- **Dependencies** that are real. Only add an edge when the second bead
  genuinely cannot start until the first lands — a graph that over-constrains
  looks tidy and starves the queue.

Mark anything a human must decide as `type: decision` and label it
`human-only`, so the coordinator never hands it to a builder.

## 3. Dry-run, always

Show the graph — indented, with the dependency edges visible — and stop. Do
not create anything until the human has read it.

Expect edits. Beads proposed by a machine skew toward too many, too small, and
too confident about ordering. Cutting a proposed plan in half is a normal
outcome, not a failure.

## 4. Create, then verify

Only after approval:

```bash
bd create --graph plan.json --dry-run   # confirm bd agrees
bd create --graph plan.json
bd dep cycles                            # a graph that cycles will never drain
```

Report what was created with its ids, and `bd ready` so the human can see what
became immediately workable.

## The dogfood test

This skill should be able to reproduce something close to this repo's own
epics from SPEC.md's Chapter 2. If it can't, it isn't good enough yet — that
plan was built by hand and is the benchmark.
