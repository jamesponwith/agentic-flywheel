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
	"strings"
)

type Roster struct {
	Caps   Caps    `json:"caps"`
	Repos  []Repo  `json:"repos"`
	Agents []Agent `json:"agents"`
}

// Caps derive from review capacity, not compute (ADR 0006).
type Caps struct {
	PRsPerNight        int `json:"prs_per_night"`
	ConcurrentBuilders int `json:"concurrent_builders"`
	ReposPerNight      int `json:"repos_per_night"`
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
	if r.Caps.PRsPerNight <= 0 || r.Caps.ConcurrentBuilders <= 0 || r.Caps.ReposPerNight <= 0 {
		return r, fmt.Errorf("%s: caps must all be positive — see ADR 0006", path)
	}
	return r, nil
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
