# 0010. Agents are a role, not a vendor

Date: 2026-08-16
Status: accepted
Amends: 0004

## Context

ADR 0004 said "Claude Code owns execution". That was true and useful, but it
wrote a *vendor* into the architecture where it meant a *role*. An audit found
the coupling was narrow but load-bearing: `run.go` hardcoded
`exec.Command("claude", …)`, the pre-push hook invoked `claude -p`, and the
skills live in one tool's format. Everything else — beads, blackbird, the Go
tools, the workflows, the ADRs — was already portable, and blackbird itself
ships adapters for three different agent clients.

The pre-push bug made the cost concrete: a hardcoded invocation broke, stayed
broken for the panel's entire life, and nobody could see it.

## Decision

The execution layer is a **declared runner**, not a named product. The roster
names the command and how to pass a prompt:

```json
"runner": { "cmd": "claude", "args": ["-p", "{prompt}"], "stdin": "none" }
```

`{prompt}` is substituted; `stdin: none` redirects `/dev/null`, which is what
git hooks require. `FLYWHEEL_AGENT_CMD` overrides the command everywhere,
including in shell hooks. Defaults keep working with no config.

Skills stay in Claude Code's format and location, because no cross-vendor
skill format exists yet and inventing one would be worse than the coupling. The
*invocation* is portable; the *prompt content* is not, and that is recorded
rather than pretended away.

## Consequences

A different agent CLI is a roster edit, not a code change, and the framework
can be shown running under a stub — which is how the hook fix was verified
without spending tokens. Harder: the runner contract is one more thing to keep
honest, and skills remain the real portability gap. When a cross-vendor skill
format appears, that is the next ADR.
