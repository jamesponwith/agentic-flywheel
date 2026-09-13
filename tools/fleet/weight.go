// Review weight (ADR 0009). The budget tracks judgement, not PR count.
package main

import (
	"strings"
)

// DefaultWeight is what an unweighted bead costs. Ordinary work, some
// judgement — the middle of the scale, so forgetting to weight a bead is
// neither free nor prohibitive.
const DefaultWeight = 2

// MaxWeight is the top of the scale: a new subsystem, or something hard to
// reverse. Nothing costs more than this, because anything that would should
// have been split into beads that can stand alone.
const MaxWeight = 3

// Weight reads a bead's `w:N` label. Unlabelled beads cost DefaultWeight, and
// an out-of-range label is clamped rather than rejected — a typo should not
// stop the night, it should just not buy unlimited review.
func Weight(b Bead) int {
	for _, l := range b.Labels {
		v, ok := strings.CutPrefix(l, "w:")
		if !ok {
			continue
		}
		switch v {
		case "1":
			return 1
		case "2":
			return 2
		case "3":
			return 3
		}
		return DefaultWeight
	}
	return DefaultWeight
}

// weightOf sums the review weight of an allocation plan.
func weightOf(as []Assignment) int {
	n := 0
	for _, a := range as {
		n += a.Weight
	}
	return n
}

// ghReviewLoad reports the review weight already open in a repo, by asking gh
// for open PRs and resolving each to its bead's weight where it can.
//
// A PR whose bead cannot be resolved counts DefaultWeight rather than zero:
// the safe error is to over-estimate pressure and start less work, never to
// under-estimate it and pile onto a queue nobody can clear.
//
// A draft PR is excluded entirely, not counted at DefaultWeight: GitHub's own
// draft state means "not ready for review", so it is not review pressure yet
// by definition, any more than an open issue with no PR is. Counting it
// anyway inflated InReview to 18 against a budget of 10 on a real roster —
// five of phux's seven open PRs were drafts — and blocked every repo's
// allocation, including a bead with nothing actually wrong with it (fw-w0u).
func ghReviewLoad(repo Repo) int {
	var prs []openPR
	// Cannot tell: assume the queue is clear rather than blocking the fleet on
	// a flaky network. The kill switch is the tool for stopping.
	if ghJSON(repo, []string{"pr", "list", "--state", "open"}, "headRefName,isDraft", &prs) != nil {
		return 0
	}
	return reviewLoadOf(prs, bdClient{dir: repo.Path, run: execBD})
}

// openPR is the slice of `gh pr list --json` this package reads.
type openPR struct {
	HeadRefName string `json:"headRefName"`
	IsDraft     bool   `json:"isDraft"`
}

// beadShower is the one bdClient method reviewLoadOf needs — narrowed so a
// test can fake it without a real repo.
type beadShower interface {
	show(id string) (Bead, error)
}

// reviewLoadOf sums review weight across prs. Split from ghReviewLoad so the
// counting logic — the part that matters — is testable without shelling out
// to gh (fw-w0u).
func reviewLoadOf(prs []openPR, bd beadShower) int {
	total := 0
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		id, ok := strings.CutPrefix(pr.HeadRefName, "bead/")
		if !ok {
			total += DefaultWeight
			continue
		}
		b, err := bd.show(id)
		if err != nil {
			total += DefaultWeight
			continue
		}
		total += Weight(b)
	}
	return total
}
