// Telling the builder the shape of its own run.
//
// A builder cannot read its environment. The sandbox refuses
// /proc/self/environ outright — "may expose secrets" — and `env`, `printenv`,
// `awk ENVIRON[...]`, `node -e process.env` and a six-line `go run` are all
// outside the allowlist, so a non-interactive run can reach none of them. Two
// consecutive runs were lost proving that, the second one after the identity
// had been placed in the environment specifically for it (fw-eoi, then fw-k8f).
//
// The proposed fix was a `guard.sh identity` subcommand that prints the token,
// which works because guard.sh is allowlisted whole. That is exactly the hole
// ADR 0003's own amendment describes — every deny rule reachable through an
// allowed prefix — and it would route a credential around a sandbox rule whose
// entire purpose is to keep credentials out of a model's context. Not that.
//
// Instead: put what the builder must ACT on in a file inside its worktree,
// which it can read, and keep credentials out of it entirely.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RunContext is the shape of one builder's run. It carries no secrets and is
// deliberately readable — it lands in the worktree, which is disposable.
type RunContext struct {
	Bead string `json:"bead"`
	Repo string `json:"repo"`
	// Solo is true when this builder is the only one that can be running.
	// Territory reservations exist to stop two agents editing the same file;
	// with a single builder there is no second agent, so requiring one buys
	// no safety and has cost every run so far. When concurrency rises the
	// requirement comes back on its own — this is a fact about the run, not a
	// switch someone remembered to flip.
	Solo bool `json:"solo"`
	// ConcurrentBuilders is the cap Solo was derived from, so a reader can
	// check the reasoning rather than trust the flag.
	ConcurrentBuilders int `json:"concurrent_builders"`
}

// writeRunContext drops the context into the worktree's .flywheel directory.
func writeRunContext(worktree string, a Assignment, opts RunOpts) error {
	rc := RunContext{
		Bead:               a.Bead,
		Repo:               a.Repo,
		Solo:               opts.Concurrency <= 1,
		ConcurrentBuilders: opts.Concurrency,
	}
	dir := filepath.Join(worktree, ".flywheel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "run-context.json"), append(b, '\n'), 0o644)
}
