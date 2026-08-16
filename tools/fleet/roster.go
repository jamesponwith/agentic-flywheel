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
)

type Roster struct {
	// WorkspaceRoot is prepended to any relative repo path. Defaults to
	// $FLYWHEEL_WORKSPACE, then ~/Workspace. Keeping it out of the committed
	// paths is what makes the roster portable — and stops a public repo
	// publishing the maintainer's home directory layout.
	WorkspaceRoot string  `json:"workspace_root,omitempty"`
	Caps          Caps    `json:"caps"`
	Repos         []Repo  `json:"repos"`
	Agents        []Agent `json:"agents"`
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
}

type Repo struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	Lang           string `json:"lang"`
	BudgetUSDMonth int    `json:"budget_usd_month"`
	Paused         bool   `json:"paused,omitempty"`
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
