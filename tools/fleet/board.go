// Board reconciliation (fw-y1y, ADR 0016).
//
// ADR 0003 forbids the fleet merging, so every merge happens outside the loop,
// and nothing carried the result back: fw-d20 was dispatched an hour after its
// PR landed. A gh:pr gate does the opposite of what is needed — `bd gate check`
// closes the gate and releases the bead back into ready (fw-62j) — so the merge
// is carried back explicitly, before every allocation.
package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PR is the subset of a pull request the board reconciler reasons about.
type PR struct {
	Number      int    `json:"number"`
	State       string `json:"state"` // OPEN | CLOSED | MERGED, as gh reports it
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
}

// prLister lists a repo's pull requests. Injected so tests never touch gh.
type prLister func(repo Repo) ([]PR, error)

// ghPRs asks gh for every PR in the repo, whatever its state. Open ones are
// wanted too: a bead with a merged PR and a still-open one is mid-stack, and
// closing it would hide the child from review.
func ghPRs(repo Repo) ([]PR, error) {
	// ponytail: --limit 200 is a ceiling on PRs read, most recent first. The
	// beads that matter are the ones whose PR merged recently, and 200 is
	// months of this fleet's throughput. The upgrade is --search with a
	// merged:> date, once the list is long enough to page.
	out, err := exec.Command("gh", "pr", "list", "--repo", "jamesponwith/"+repo.Name,
		"--state", "all", "--limit", "200", "--json", "number,state,title,headRefName").Output()
	if err != nil {
		// Keep stderr: "auth expired", "rate limited" and "no such repo" are
		// different problems, and the caller's refusal to plan should say which.
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh pr list: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	return prs, nil
}

// BoardClose is one bead the reconciler acted on, or deliberately did not.
type BoardClose struct {
	Repo   string `json:"repo"`
	Bead   string `json:"bead"`
	PR     int    `json:"pr"`
	Action string `json:"action"` // closed | would-close | kept | failed
	Detail string `json:"detail"`
}

// ReconcileBoard closes every open or in-progress bead whose PR has merged.
// Without execute it only reports, as "would-close" — the same convention as
// run: a command that mutates the board should show its hand first.
//
// A listing failure is an error, not an empty list: "gh is down" and "nothing
// merged" must not look alike, because the second one dispatches builders.
//
// ponytail: a bead a human reopened after its PR merged is closed again on the
// next cycle, because the PR stays MERGED forever. Reverted or broken work
// gets a new bead — one PR is one idea (ADR 0009), and a reopened one would
// be a second idea under the first's name. A fork PR titled after a bead can
// force "kept" and a rebuild; this repo takes no outside PRs, and the cost is
// the pre-existing behaviour, not a new one.
func ReconcileBoard(repo Repo, bd bdClient, list prLister, execute bool) ([]BoardClose, error) {
	prs, err := list(repo)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repo.Name, err)
	}
	var beads []Bead
	for _, status := range []string{"open", "in_progress"} {
		bs, err := bd.list(status)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", repo.Name, err)
		}
		beads = append(beads, bs...)
	}

	var out []BoardClose
	for _, b := range beads {
		var merged, open []PR
		for _, pr := range prs {
			if !names(pr, b.ID) {
				continue
			}
			switch pr.State {
			case "MERGED":
				merged = append(merged, pr)
			case "OPEN":
				open = append(open, pr)
			}
		}
		if len(merged) == 0 {
			continue // nothing landed; not the reconciler's business
		}
		c := BoardClose{Repo: repo.Name, Bead: b.ID, PR: merged[0].Number}
		if len(open) > 0 {
			// A merged parent with an open child is a stack still in review.
			// The bead is not done until the last PR lands, and closing it now
			// would drop the child out of the review-load count (weight.go).
			c.Action = "kept"
			c.Detail = fmt.Sprintf("PR #%d merged but #%d is still open — closes when the stack lands",
				merged[0].Number, open[0].Number)
			out = append(out, c)
			continue
		}
		if !execute {
			c.Action, c.Detail = "would-close", fmt.Sprintf("PR #%d merged: %s — pass -execute to close", merged[0].Number, merged[0].Title)
			out = append(out, c)
			continue
		}
		reason := fmt.Sprintf("PR #%d merged: %s — closed by fleet reconcile-board; the fleet never merges, so this is how the merge reaches the board (fw-y1y)",
			merged[0].Number, merged[0].Title)
		if err := bd.close(b.ID, reason); err != nil {
			c.Action, c.Detail = "failed", err.Error()
			out = append(out, c)
			continue
		}
		c.Action, c.Detail = "closed", fmt.Sprintf("PR #%d merged: %s", merged[0].Number, merged[0].Title)
		out = append(out, c)
	}
	return out, nil
}

// names reports whether a PR is about the bead: cut on the bead's branch (the
// spawner's convention, run.go), or titled the way this repo titles PRs —
// `<id>: …` or `… (<id>)`. Only those two shapes: a builder writes its own PR
// title, and a looser rule (any id mentioned anywhere) would let "fw-abc: …,
// also fw-def" close fw-def the moment a human merged fw-abc — a bead closed by
// an agent that never built it, laundered through a review of the diff rather
// than the title (security lens). The shapes also bound the id exactly, so
// fw-d20 never matches fw-d20.1 or the reverse.
//
// A revert's title quotes the original, so it would carry the id and close
// the bead as done — the one PR that means the opposite.
func names(pr PR, id string) bool {
	if pr.HeadRefName == "bead/"+id {
		return true
	}
	if strings.HasPrefix(pr.Title, "Revert ") {
		return false
	}
	return strings.HasPrefix(pr.Title, id+":") || strings.Contains(pr.Title, "("+id+")")
}
