// The roster: which repos are in the fleet, which agents exist, and the caps
// that keep the night's output reviewable (ADR 0006).
//
// A committed file, not a database — it should be readable in ten seconds and
// diffable in review, because it is the document that says what the machines
// are allowed to do.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Roster struct {
	// WorkspaceRoot is prepended to any relative repo path. Defaults to
	// $FLYWHEEL_WORKSPACE, then ~/Workspace. Keeping it out of the committed
	// paths is what makes the roster portable — and stops a public repo
	// publishing the maintainer's home directory layout.
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	Caps          Caps   `json:"caps"`
	// Runner is how a builder is invoked (ADR 0010). Omit for the default.
	Runner Runner  `json:"runner,omitempty"`
	Repos  []Repo  `json:"repos"`
	Agents []Agent `json:"agents"`
}

// expandPath resolves ~, $HOME, and paths relative to the workspace root.
// An absolute path is honoured as-is, so an unusual layout is still expressible.
func expandPath(p, root string) string {
	if p == "" {
		return p
	}
	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	if root == "" {
		root = os.Getenv("FLYWHEEL_WORKSPACE")
	}
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, "Workspace")
		}
	}
	return filepath.Join(expandPath(root, ""), p)
}

// Caps derive from review capacity, not compute (ADR 0006), and are measured
// in review WEIGHT rather than PR count (ADR 0009) — a 9-line CI fix and a
// 600-line coordinator are both "1 PR" and are nothing like the same review.
type Caps struct {
	// ReviewWeightPerNight is the budget. Eight trivia or two hard changes,
	// whichever the queue holds.
	ReviewWeightPerNight int `json:"review_weight_per_night"`
	ConcurrentBuilders   int `json:"concurrent_builders"`
	ReposPerNight        int `json:"repos_per_night"`

	// PRsPerNight is the superseded ADR 0006 cap. Read for migration only:
	// a roster that still sets it gets it treated as weight, with a warning.
	PRsPerNight int `json:"prs_per_night,omitempty"`

	// Quota describes what actually rations the fleet.
	Quota Quota `json:"quota"`
}

// Quota is the account's usage limit, which on a subscription is a period, not
// a bill — and one the account never states as a number.
//
// Two earlier versions guessed at it and both were wrong. Dollars per repo per
// month rationed something a subscription never charges, and paused a repo for
// a month over a quota that had already reset. Dollars per window had the right
// shape and still required a number that maps to nothing the account publishes
// — there is no command that reports remaining quota, so any figure here is a
// guess wearing a unit.
//
// WindowHours is therefore a reporting period, not a limit: it says how far
// back "recent usage" looks. What actually paces the fleet is the account's own
// 429 and the reset time it carries (quotahold.go).
type Quota struct {
	// WindowHours is how long until the usage period resets. The 429 that
	// killed the first night named its own reset time ("resets 6pm").
	WindowHours int `json:"window_hours"`
}

// window is the plan's reset period, defaulting to the 5-hour session window a
// Max subscription rations on.
func (q Quota) window() time.Duration {
	if q.WindowHours <= 0 {
		return 5 * time.Hour
	}
	return time.Duration(q.WindowHours) * time.Hour
}

type Repo struct {
	Name string `json:"name"`
	// Role is "instance" (default) or "template". A template legitimately has
	// no .beads — shipping one would seed every new project with someone
	// else's issues (fw-fsa.6).
	Role string `json:"role,omitempty"`
	// SkipStages are stages this repo has no use for. brax-tennis-rl is a
	// training project with nothing deployed, so an SLO prober would be
	// cargo-culting a stage into a repo that cannot use it (fw-fsa.8).
	SkipStages []string `json:"skip_stages,omitempty"`
	Path       string   `json:"path"`
	Lang       string   `json:"lang"`
	Paused     bool     `json:"paused,omitempty"`
}

type Agent struct {
	Name   string   `json:"name"`   // <repo-or-fleet>/<role>[-<lens>]
	Role   string   `json:"role"`   // coordinator|builder|reviewer|operator|retro
	Repos  []string `json:"repos"`  // repo names, or ["*"] for any
	Skills []string `json:"skills"` // matched against bead labels
}

func LoadRoster(path string) (Roster, error) {
	var r Roster
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("%s: %w", path, err)
	}
	for i := range r.Repos {
		r.Repos[i].Path = expandPath(r.Repos[i].Path, r.WorkspaceRoot)
		// Resolve symlinks once, here. blackbird keys reservations by
		// project_key: two agents that derive different keys for the same repo
		// do not conflict at all, which is the failure territory reservations
		// exist to prevent, in its most dangerous form — silent (fw-wb2.9).
		// One canonical path, decided in one place, passed to every builder.
		if resolved, err := filepath.EvalSymlinks(r.Repos[i].Path); err == nil {
			r.Repos[i].Path = resolved
		}
	}
	r.Caps = r.Caps.normalized()
	if r.Caps.ReviewWeightPerNight <= 0 || r.Caps.ConcurrentBuilders <= 0 || r.Caps.ReposPerNight <= 0 {
		return r, fmt.Errorf("%s: caps must all be positive — see ADR 0006 and 0009", path)
	}
	return r, nil
}

// normalized migrates an ADR 0006-era caps block to the ADR 0009 weight
// budget. Applied by LoadRoster and again inside Allocate, because a Roster
// built in code (a test, an embedding tool) never passes through LoadRoster —
// and a zero budget makes Allocate silently allocate nothing, which is the
// worst possible failure for a scheduler.
func (c Caps) normalized() Caps {
	if c.ReviewWeightPerNight <= 0 && c.PRsPerNight > 0 {
		c.ReviewWeightPerNight = c.PRsPerNight
	}
	return c
}

func (r Roster) Repo(name string) (Repo, bool) {
	for _, repo := range r.Repos {
		if repo.Name == name {
			return repo, true
		}
	}
	return Repo{}, false
}

// Match returns the agents eligible for a bead in a repo: right role, covers
// the repo, and holds every skill the bead's labels demand.
//
// Skill demands come from labels of the form `lang:go` or `skill:jax`; a bead
// with no such labels can go to any agent with the role.
func (r Roster) Match(role, repo string, b Bead) []Agent {
	var out []Agent
	for _, a := range r.Agents {
		if a.Role != role || !a.covers(repo) {
			continue
		}
		if a.hasAll(requiredSkills(b)) {
			out = append(out, a)
		}
	}
	return out
}

func requiredSkills(b Bead) []string {
	var want []string
	for _, l := range b.Labels {
		if k, v, ok := strings.Cut(l, ":"); ok && (k == "lang" || k == "skill") {
			want = append(want, v)
		}
	}
	return want
}

func (a Agent) covers(repo string) bool {
	for _, r := range a.Repos {
		if r == "*" || r == repo {
			return true
		}
	}
	return false
}

func (a Agent) hasAll(want []string) bool {
	for _, w := range want {
		found := false
		for _, s := range a.Skills {
			if s == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
