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
	Number int    `json:"number"`
	State  string `json:"state"` // OPEN | CLOSED | MERGED, as gh reports it
	Title  string `json:"title"`
	// HeadRefName is the branch the work is on; BaseRefName is the branch it
	// merged into. gh's MERGED means "merged somewhere", so without the base
	// the reconciler cannot tell a landing from a detour (fw-ojk).
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
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
		"--state", "all", "--limit", "200", "--json", "number,state,title,headRefName,baseRefName").Output()
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

// ghDefaultBranch asks gh which branch the repo ships from. Not hardcoded to
// main: the roster is portable, and a repo that shipped from master would have
// every one of its merges read as a detour. Asked rather than configured — the
// same gh that reports the merge reports where it landed, so the two answers
// cannot disagree.
func ghDefaultBranch(repo Repo) (string, error) {
	out, err := exec.Command("gh", "repo", "view", "jamesponwith/"+repo.Name,
		"--json", "defaultBranchRef").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("gh repo view: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("gh repo view: %w", err)
	}
	var v struct {
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("gh repo view: %w", err)
	}
	if v.DefaultBranchRef.Name == "" {
		return "", fmt.Errorf("gh repo view: %s reports no default branch", repo.Name)
	}
	return v.DefaultBranchRef.Name, nil
}

// BoardClose is one bead the reconciler acted on, or deliberately did not.
type BoardClose struct {
	Repo   string `json:"repo"`
	Bead   string `json:"bead"`
	PR     int    `json:"pr"`
	Action string `json:"action"` // closed | would-close | kept | merged-elsewhere | closed-elsewhere | failed
	Detail string `json:"detail"`
}

// oneLine flattens a PR title for a report line. Board lines are tee'd into the
// committed nightly digest (nightly.sh) and read back by a human and by the
// retro skill, so a title carrying a newline could forge a second board line —
// "… closed  PR #7 merged" under a bead nothing closed. Titles are written by
// agents, which makes this cheaper to prevent than to notice (fw-ojk).
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ReconcileBoard closes every open or in-progress bead whose PR has merged.
// Without execute it only reports, as "would-close" — the same convention as
// run: a command that mutates the board should show its hand first.
//
// A merge is only a merge onto repo.DefaultBranch. gh reports MERGED for a PR
// that landed anywhere, and a bead closed on a merge into a feature branch says
// done while the default branch does not have the work — silent loss, worse
// than the drift this fixes (fw-ojk). Such a bead is reported as
// "merged-elsewhere" and left open; one already closed that way is reported as
// "closed-elsewhere". An unknown default branch is a hard error for the same
// reason a failed listing is: guessing costs the work.
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
	if repo.DefaultBranch == "" {
		return nil, fmt.Errorf("%s: default branch unknown — cannot tell a merge that ships from one into a feature branch, and closing on the second loses the work (fw-ojk)", repo.Name)
	}
	prs, err := list(repo)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repo.Name, err)
	}
	// A MERGED PR with no base is gh not answering what it was asked. Refused
	// loudly rather than absorbed: absorbed, every merge reads as a detour,
	// nothing ever closes, and the fw-y1y drift comes back disguised as a
	// per-bead oddity instead of the total outage it is.
	for _, pr := range prs {
		if pr.State == "MERGED" && pr.BaseRefName == "" {
			return nil, fmt.Errorf("%s: PR #%d is MERGED with no base branch — cannot tell where anything landed", repo.Name, pr.Number)
		}
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
		merged, elsewhere, open := landing(prs, b.ID, repo.DefaultBranch)
		if len(merged) == 0 {
			if len(elsewhere) == 0 {
				continue // nothing landed anywhere; not the reconciler's business
			}
			// Merged, but not onto the branch that ships. Never a close, and
			// reported rather than skipped: the cause is opening a PR with a
			// non-default branch checked out, so gh infers the base from HEAD,
			// and nothing else in the loop would ever say so (fw-ojk).
			out = append(out, BoardClose{
				Repo: repo.Name, Bead: b.ID, PR: elsewhere[0].Number, Action: "merged-elsewhere",
				Detail: fmt.Sprintf("PR #%d merged into %s, not %s — the work is not on the default branch: %s",
					elsewhere[0].Number, elsewhere[0].BaseRefName, repo.DefaultBranch, oneLine(elsewhere[0].Title)),
			})
			continue
		}
		// A bead can have both: a small PR onto the default branch and the real
		// work merged elsewhere. The bead still closes — something did land —
		// but the close says where the rest went, because a builder writes its
		// own PR title and this is otherwise exactly fw-ojk's silent loss with
		// one extra PR in front of it.
		detour := ""
		if len(elsewhere) > 0 {
			detour = fmt.Sprintf(" — NOTE: PR #%d for this bead merged into %s, not %s",
				elsewhere[0].Number, elsewhere[0].BaseRefName, repo.DefaultBranch)
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
			c.Action, c.Detail = "would-close", fmt.Sprintf("PR #%d merged: %s — pass -execute to close%s", merged[0].Number, oneLine(merged[0].Title), detour)
			out = append(out, c)
			continue
		}
		reason := fmt.Sprintf("PR #%d merged: %s — closed by fleet reconcile-board; the fleet never merges, so this is how the merge reaches the board (fw-y1y)%s",
			merged[0].Number, oneLine(merged[0].Title), detour)
		if err := bd.close(b.ID, reason); err != nil {
			c.Action, c.Detail = "failed", err.Error()
			out = append(out, c)
			continue
		}
		c.Action, c.Detail = "closed", fmt.Sprintf("PR #%d merged: %s%s", merged[0].Number, oneLine(merged[0].Title), detour)
		out = append(out, c)
	}
	return append(out, alreadyClosedElsewhere(repo, bd, prs)...), nil
}

// alreadyClosedElsewhere reports beads this rule was written too late for: the
// board's past, not just its future. spot-2ig was closed on PR #4 — 1880 lines
// into fleet/builder-permissions — before a merge had to reach the default
// branch to count, so a fix that only prevents the next one leaves the work
// exactly as invisible as it was. Reports, never reopens: whether that work is
// recovered by landing the branch or rebuilt is a human's call (ADR 0003).
//
// A listing failure here is not an error. Being unable to look at the past must
// not stop the present being reconciled, which is the caller's real job.
//
// ponytail: the closed list is asked for only when a detour exists at all, so a
// healthy repo pays nothing. In one that has a detour it is a full closed-bead
// scan on a path that runs before every allocate; the upgrade, if that ever
// bites, is to ask bd for the closed beads named by those PRs alone.
func alreadyClosedElsewhere(repo Repo, bd bdClient, prs []PR) []BoardClose {
	detour := false
	for _, pr := range prs {
		if pr.State == "MERGED" && pr.BaseRefName != repo.DefaultBranch {
			detour = true
			break
		}
	}
	if !detour {
		return nil
	}
	closed, err := bd.list("closed")
	if err != nil {
		return nil
	}
	var out []BoardClose
	for _, b := range closed {
		merged, elsewhere, _ := landing(prs, b.ID, repo.DefaultBranch)
		if len(elsewhere) == 0 || len(merged) > 0 {
			continue // never landed off-default, or landed properly as well
		}
		out = append(out, BoardClose{
			Repo: repo.Name, Bead: b.ID, PR: elsewhere[0].Number, Action: "closed-elsewhere",
			Detail: fmt.Sprintf("closed, but PR #%d merged into %s, not %s — %s does not have the work: %s",
				elsewhere[0].Number, elsewhere[0].BaseRefName, repo.DefaultBranch, repo.DefaultBranch, oneLine(elsewhere[0].Title)),
		})
	}
	return out
}

// landing splits the PRs that name a bead into those that reached the branch
// the repo ships from, those that merged somewhere else, and those still open.
func landing(prs []PR, id, defaultBranch string) (merged, elsewhere, open []PR) {
	for _, pr := range prs {
		if !names(pr, id) {
			continue
		}
		switch pr.State {
		case "MERGED":
			if pr.BaseRefName == defaultBranch {
				merged = append(merged, pr)
			} else {
				elsewhere = append(elsewhere, pr)
			}
		case "OPEN":
			open = append(open, pr)
		}
	}
	return merged, elsewhere, open
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
