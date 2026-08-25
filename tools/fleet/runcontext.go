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
	// Solo is true when this builder is the only one that can be running IN
	// THIS REPO. That is the bound reservations actually care about: blackbird
	// keys them by project_key, so two builders in different repos cannot
	// collide however many of them there are. Serialising per repo therefore
	// lets the fleet widen across repos without re-arming a requirement that
	// has cost three runs and bought no conflict (fw-k8f).
	//
	// Derived, never configured. Raising builders-per-repo above one brings
	// the requirement back by itself.
	Solo bool `json:"solo"`
	// BuildersInThisRepo is what Solo was derived from, so a reader can check
	// the reasoning rather than trust the flag.
	BuildersInThisRepo int `json:"builders_in_this_repo"`
	// FleetWidth is how many builders may run at once across all repos. It
	// does not affect Solo and is here so a report can say what the run's
	// shape was.
	FleetWidth int `json:"fleet_width"`
}

// writeRunContext drops the context into the worktree's .flywheel directory.
func writeRunContext(worktree string, a Assignment, opts RunOpts, buildersHere, width int) error {
	rc := RunContext{
		Bead:               a.Bead,
		Repo:               a.Repo,
		Solo:               buildersHere <= 1,
		BuildersInThisRepo: buildersHere,
		FleetWidth:         width,
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
