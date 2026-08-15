// The coordinator: decide what the fleet works on tonight.
//
// It allocates and never edits code. That separation is what makes its
// decisions auditable and its failures survivable — a coordinator bug wastes a
// night, it does not corrupt a repository.
//
// Every declined bead is reported with a reason. Silent truncation would read
// as "there was nothing to do", which is the one lie that would make the fleet
// untrustworthy.
package main

import (
	"fmt"
	"sort"
	"time"
)

type Assignment struct {
	Repo  string `json:"repo"`
	Bead  string `json:"bead"`
	Title string `json:"title"`
	Agent string `json:"agent"`
}

type Declined struct {
	Repo   string `json:"repo"`
	Bead   string `json:"bead"`
	Reason string `json:"reason"`
}

type Plan struct {
	GeneratedAt time.Time    `json:"generated_at"`
	Assignments []Assignment `json:"assignments"`
	Declined    []Declined   `json:"declined"`
	Caps        Caps         `json:"caps"`
	// Stopped is non-empty when the kill switch halted this cycle.
	Stopped string `json:"stopped,omitempty"`
}

// openClient opens a bd client for a repo path. Injected so tests can supply
// fixtures instead of five real repositories.
type openClient func(path string) bdClient

// Allocate produces the night's plan. It does not spawn anything: handing back
// a plan means the caller can print it, diff it, or run it, and a dry run is
// the default rather than a special mode.
func Allocate(r Roster, open openClient, now time.Time) (Plan, error) {
	plan := Plan{GeneratedAt: now.UTC(), Caps: r.Caps}

	// The kill switch outranks everything, including work already ready.
	if why, halted := stopped(r.activePaths()); halted {
		plan.Stopped = why
		return plan, nil
	}

	reposUsed := map[string]bool{}
	for _, repo := range r.Repos {
		if repo.Paused {
			plan.Declined = append(plan.Declined, Declined{Repo: repo.Name, Reason: "repo paused in roster"})
			continue
		}
		if len(plan.Assignments) >= r.Caps.PRsPerNight {
			plan.Declined = append(plan.Declined, Declined{Repo: repo.Name,
				Reason: fmt.Sprintf("nightly PR cap reached (%d)", r.Caps.PRsPerNight)})
			continue
		}
		if len(reposUsed) >= r.Caps.ReposPerNight && !reposUsed[repo.Name] {
			plan.Declined = append(plan.Declined, Declined{Repo: repo.Name,
				Reason: fmt.Sprintf("repo cap reached (%d active repos/night)", r.Caps.ReposPerNight)})
			continue
		}

		beads, err := open(repo.Path).ready()
		if err != nil {
			// One unreachable repo must not sink the whole night.
			plan.Declined = append(plan.Declined, Declined{Repo: repo.Name, Reason: "bd ready failed: " + err.Error()})
			continue
		}
		beads = allocatable(beads, now)
		sort.SliceStable(beads, func(i, j int) bool { return beads[i].Priority < beads[j].Priority })

		for _, b := range beads {
			if len(plan.Assignments) >= r.Caps.PRsPerNight {
				plan.Declined = append(plan.Declined, Declined{Repo: repo.Name, Bead: b.ID,
					Reason: fmt.Sprintf("nightly PR cap reached (%d)", r.Caps.PRsPerNight)})
				break
			}
			if countFor(plan.Assignments, repo.Name) >= r.Caps.ConcurrentBuilders {
				plan.Declined = append(plan.Declined, Declined{Repo: repo.Name, Bead: b.ID,
					Reason: fmt.Sprintf("concurrent builder cap reached (%d)", r.Caps.ConcurrentBuilders)})
				break
			}
			agents := r.Match("builder", repo.Name, b)
			if len(agents) == 0 {
				plan.Declined = append(plan.Declined, Declined{Repo: repo.Name, Bead: b.ID,
					Reason: "no builder in the roster has the required skills"})
				continue
			}
			plan.Assignments = append(plan.Assignments, Assignment{
				Repo: repo.Name, Bead: b.ID, Title: b.Title, Agent: agents[0].Name,
			})
			reposUsed[repo.Name] = true
		}
	}
	return plan, nil
}

// allocatable filters out what an unattended builder must not take: epics
// (containers, not work), decisions (a human's call), and anything already
// under a live lease.
func allocatable(in []Bead, now time.Time) []Bead {
	var out []Bead
	for _, b := range in {
		if b.Type == "epic" || b.Type == "decision" {
			continue
		}
		if b.HasLabel("human-only") {
			continue
		}
		if _, expires, ok := b.Lease(); ok && expires.After(now) {
			continue
		}
		out = append(out, b)
	}
	return out
}

func countFor(as []Assignment, repo string) int {
	n := 0
	for _, a := range as {
		if a.Repo == repo {
			n++
		}
	}
	return n
}
