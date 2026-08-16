// The declared runner (ADR 0010). The fleet spawns "an agent", not a product.
package main

import (
	"os"
	"strings"
)

// Runner describes how to invoke an agent CLI. Declared in the roster so a
// different agent is a config edit rather than a code change.
type Runner struct {
	Cmd   string   `json:"cmd"`
	Args  []string `json:"args"`  // "{prompt}" is substituted
	Stdin string   `json:"stdin"` // "none" redirects /dev/null; anything else inherits
}

// DefaultRunner is Claude Code, because that is what is installed here — but
// it is a default, not an assumption baked into the call site.
func DefaultRunner() Runner {
	return Runner{Cmd: "claude", Args: []string{"-p", "{prompt}"}, Stdin: "none"}
}

// resolved returns the runner with env overrides applied and defaults filled.
//
// FLYWHEEL_AGENT_CMD overrides the command everywhere — including in shell
// hooks, so one variable switches the whole flywheel to another agent.
func (r Runner) resolved() Runner {
	if r.Cmd == "" {
		r = DefaultRunner()
	}
	if env := os.Getenv("FLYWHEEL_AGENT_CMD"); env != "" {
		r.Cmd = env
	}
	if len(r.Args) == 0 {
		r.Args = DefaultRunner().Args
	}
	return r
}

// argv renders the command line for a prompt. A runner whose args never
// mention {prompt} gets it appended, so a minimal `{"cmd": "myagent"}` still
// receives the work rather than silently running with no instructions.
func (r Runner) argv(prompt string) []string {
	r = r.resolved()
	out := make([]string, 0, len(r.Args)+1)
	found := false
	for _, a := range r.Args {
		if strings.Contains(a, "{prompt}") {
			found = true
			a = strings.ReplaceAll(a, "{prompt}", prompt)
		}
		out = append(out, a)
	}
	if !found {
		out = append(out, prompt)
	}
	return out
}

// quietStdin reports whether stdin must be /dev/null. Git hands a pre-push
// hook the ref list on stdin, which an agent CLI will read instead of its
// prompt — the bug that kept the review panel dead for its whole life.
func (r Runner) quietStdin() bool { return r.resolved().Stdin != "inherit" }
