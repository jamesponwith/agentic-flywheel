// Board reconciliation (fw-y1y, ADR 0016).
//
// ADR 0003 forbids the fleet merging, so every merge happens outside the loop
// and nothing carried the result back to the board. fw-d20, fw-6gc and fw-0rf
// all had merged PRs and were still open or in progress; the next plan included
// fw-d20, whose PR had merged an hour earlier, and a builder would have spent
// ~$7 rebuilding work already on main and opened a second PR for it.
//
// The obvious tool, a gh:pr gate on the bead, does the opposite of what is
// needed: `bd gate check` closes the GATE when the PR merges and releases what
// the gate blocked — back into `bd ready`. Proven on fw-62j: PR 24 merged, the
// gate closed, and fw-fsa.9 became ready again, not closed. A gate is for "do
// not start until X lands"; this is "X landed, so this is done".
//
// So the merge is carried back explicitly: for every open or in-progress bead,
// ask gh whether a PR naming it has merged, and close the bead with the PR as
// the reason. Runs before every allocation, so the board cannot drift from
// main between a human's merge and the coordinator's next dispatch.
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
// spawner's convention, run.go), or naming the id in its title.
func names(pr PR, id string) bool {
	return pr.HeadRefName == "bead/"+id || mentions(pr.Title, id)
}

// mentions finds id in s as a whole token. Bead ids nest — fw-d20 is not
// fw-d20.1 and must not close it — so a neighbouring id character disqualifies
// the match, except a full stop that ends a sentence rather than an id.
func mentions(s, id string) bool {
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], id)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(id)
		before := start == 0 || !idChar(s[start-1])
		after := end == len(s) || !idChar(s[end]) ||
			(s[end] == '.' && (end+1 == len(s) || !idChar(s[end+1])))
		if before && after {
			return true
		}
		from = start + 1
	}
	return false
}

func idChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.'
}
