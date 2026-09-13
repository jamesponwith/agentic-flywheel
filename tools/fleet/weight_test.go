package main

import "testing"

// fakeBeadShower answers show() from a fixed map, keyed by bead id, so
// reviewLoadOf can be tested without a real repo or bd binary.
type fakeBeadShower map[string]Bead

func (f fakeBeadShower) show(id string) (Bead, error) {
	b, ok := f[id]
	if !ok {
		return Bead{}, errNotFound{id}
	}
	return b, nil
}

// fw-w0u: a draft PR is not review pressure. GitHub's own draft state means
// "not ready for review" — counting it anyway inflated a real roster's
// InReview to 18 against a budget of 10 and blocked every repo's allocation.
func TestReviewLoadOfExcludesDrafts(t *testing.T) {
	bd := fakeBeadShower{"x-1": {Labels: []string{"w:3"}}}
	prs := []openPR{
		{HeadRefName: "bead/x-1", IsDraft: false},          // resolves to w:3
		{HeadRefName: "bead/x-1", IsDraft: true},           // same bead, but draft — excluded
		{HeadRefName: "some/other-branch", IsDraft: false}, // unresolvable — DefaultWeight
		{HeadRefName: "some/other-branch", IsDraft: true},  // unresolvable AND draft — excluded
	}
	got := reviewLoadOf(prs, bd)
	want := 3 + DefaultWeight // only the two non-draft PRs count
	if got != want {
		t.Errorf("reviewLoadOf = %d, want %d (a draft PR must not count as review pressure)", got, want)
	}
}

func TestReviewLoadOfAllDraftsIsZero(t *testing.T) {
	prs := []openPR{
		{HeadRefName: "bead/x-1", IsDraft: true},
		{HeadRefName: "some/other-branch", IsDraft: true},
	}
	if got := reviewLoadOf(prs, fakeBeadShower{}); got != 0 {
		t.Errorf("reviewLoadOf = %d, want 0 — a queue of nothing but drafts is not review pressure", got)
	}
}

func TestReviewLoadOfUnresolvableBeadCostsDefaultWeight(t *testing.T) {
	// The safe error is to over-estimate pressure, never under-estimate it.
	prs := []openPR{{HeadRefName: "bead/missing", IsDraft: false}}
	if got := reviewLoadOf(prs, fakeBeadShower{}); got != DefaultWeight {
		t.Errorf("reviewLoadOf = %d, want %d for an unresolvable bead", got, DefaultWeight)
	}
}
