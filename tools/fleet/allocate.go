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
	Repo   string `json:"repo"`
	Bead   string `json:"bead"`
	Title  string `json:"title"`
	Agent  string `json:"agent"`
	Weight int    `json:"weight"`
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
	// Weight budget accounting (ADR 0009). InReview is weight already sitting
	// in open PRs: a full queue reduces what tonight may start, which is the
	// constraint enforcing itself rather than being remembered.
	Allocated int `json:"allocated_weight"`
	InReview  int `json:"in_review_weight"`
	Budget    int `json:"budget_weight"`
	// Stopped is non-empty when the kill switch halted this cycle.
	Stopped string `json:"stopped,omitempty"`
}

// openClient opens a bd client for a repo path. Injected so tests can supply
// fixtures instead of five real repositories.
type openClient func(path string) bdClient

// reviewLoad reports weight already awaiting review in a repo. Injected so
// tests never touch the network.
type reviewLoad func(repo Repo) int

// Allocate produces the night's plan. It does not spawn anything: handing back
// a plan means the caller can print it, diff it, or run it, and a dry run is
// the default rather than a special mode.
func Allocate(r Roster, open openClient, now time.Time) (Plan, error) {
	return AllocateWithLoad(r, open, nil, now)
}

// AllocateWithLoad is Allocate with review pressure accounted for.
func AllocateWithLoad(r Roster, open openClient, load reviewLoad, now time.Time) (Plan, error) {
	r.Caps = r.Caps.normalized()
	if r.Caps.ReviewWeightPerNight <= 0 {
		// Refuse rather than allocate nothing. A scheduler that silently does
		// no work looks identical to a scheduler with an empty queue.
		return Plan{GeneratedAt: now.UTC(), Caps: r.Caps}, fmt.Errorf(
			"review_weight_per_night must be positive — see ADR 0009")
	}
	plan := Plan{GeneratedAt: now.UTC(), Caps: r.Caps, Budget: r.Caps.ReviewWeightPerNight}

	// The kill switch outranks everything, including work already ready.
	if why, halted := stopped(r.activePaths()); halted {
		plan.Stopped = why
		return plan, nil
	}

	// Weight already in review is spent before tonight starts.
	if load != nil {
		for _, repo := range r.Repos {
			if !repo.Paused {
				plan.InReview += load(repo)
			}
		}
	}
	remaining := func() int { return plan.Budget - plan.InReview - weightOf(plan.Assignments) }
	if remaining() <= 0 {
		plan.Declined = append(plan.Declined, Declined{
			Reason: fmt.Sprintf("review queue is full: %d weight already open against a budget of %d — merge before starting more",
				plan.InReview, plan.Budget)})
		return plan, nil
	}

	reposUsed := map[string]bool{}
	for _, repo := range r.Repos {
		if repo.Paused {
			plan.Declined = append(plan.Declined, Declined{Repo: repo.Name, Reason: "repo paused in roster"})
			continue
		}
		if remaining() <= 0 {
			plan.Declined = append(plan.Declined, Declined{Repo: repo.Name,
				Reason: fmt.Sprintf("review weight budget spent (%d/%d)", plan.Budget-remaining(), plan.Budget)})
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
		// Sort by priority, then id. The id tiebreak is not cosmetic: without
		// it, equal-priority beads come back in whatever order bd emitted them,
		// so two runs of the same queue produce different plans. A plan you
		// cannot reproduce is a plan you cannot diff, review, or replay.
		sort.SliceStable(beads, func(i, j int) bool {
			if beads[i].Priority != beads[j].Priority {
				return beads[i].Priority < beads[j].Priority
			}
			return beads[i].ID < beads[j].ID
		})

		for _, b := range beads {
			w := Weight(b)
			if w > remaining() {
				// Not "break": a lighter bead further down the queue may still
				// fit, and skipping it would waste budget on nothing.
				plan.Declined = append(plan.Declined, Declined{Repo: repo.Name, Bead: b.ID,
					Reason: fmt.Sprintf("weight %d exceeds the %d remaining in the budget", w, remaining())})
				continue
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
				Repo: repo.Name, Bead: b.ID, Title: b.Title, Agent: agents[0].Name, Weight: w,
			})
			reposUsed[repo.Name] = true
		}
	}
	plan.Allocated = weightOf(plan.Assignments)

	// Every repo appears exactly once: as assignments, or as a decline naming
	// the reason. Silence would read as "nothing to say" (fw-oef.11).
	for _, repo := range r.Repos {
		if countFor(plan.Assignments, repo.Name) > 0 || declinedFor(plan.Declined, repo.Name) {
			continue
		}
		plan.Declined = append(plan.Declined, Declined{Repo: repo.Name, Reason: "no ready work"})
	}
	return plan, nil
}

func declinedFor(ds []Declined, repo string) bool {
	for _, d := range ds {
		if d.Repo == repo {
			return true
		}
	}
	return false
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
