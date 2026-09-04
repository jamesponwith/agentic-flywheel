//go:build conformance

package main

import (
	"strings"
	"testing"
	"time"
)

// TestConformanceMergedPRLeavesTheReadyQueue is fw-y1y's acceptance criterion
// against a real bd database: a bead whose PR merged is not dispatched again.
// It asserts on `bd ready`, which is what the coordinator reads — not on a
// function having been called.
//
// Both board states that happened for real are driven: fw-d20 was open (its
// lease had been reclaimed) and fw-6gc was still in progress.
func TestConformanceMergedPRLeavesTheReadyQueue(t *testing.T) {
	for _, status := range []string{"open", "in_progress"} {
		t.Run(status, func(t *testing.T) {
			dir := scratchRepo(t)
			id := newBead(t, dir, "work whose PR merged while the fleet was not looking")
			if status == "in_progress" {
				// Claimed under an expired lease: the builder opened its PR and
				// stopped, as the skill tells it to, and never came back.
				if err := liveLeaser(dir, time.Now().Add(-2*time.Hour)).Claim(id, "fleet/builder-go", time.Hour); err != nil {
					t.Fatal(err)
				}
			}
			if !strings.Contains(bdIn(t, dir, "ready", "--json"), id) && status == "open" {
				t.Fatalf("%s is not ready before the merge; the test proves nothing", id)
			}

			repo := Repo{Name: "scratch", Path: dir, Lang: "go", DefaultBranch: "main"}
			merged := func(Repo) ([]PR, error) {
				return []PR{{Number: 62, State: "MERGED", Title: "the work, merged by a human",
					HeadRefName: "bead/" + id, BaseRefName: "main"}}, nil
			}
			got, err := ReconcileBoard(repo, bdClient{dir: dir, run: execBD}, merged, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Bead != id || got[0].Action != "closed" {
				t.Fatalf("reconcile = %+v, want %s closed", got, id)
			}

			// The property that matters: the coordinator cannot see it.
			if ready := bdIn(t, dir, "ready", "--json"); strings.Contains(ready, id) {
				t.Errorf("%s is still in bd ready after its PR merged:\n%s", id, ready)
			}
			// Nor can reclaim resurrect it: an expired lease on a closed bead
			// must not return it to open.
			if _, err := liveLeaser(dir, time.Now()).Reclaim(); err != nil {
				t.Fatal(err)
			}
			if ready := bdIn(t, dir, "ready", "--json"); strings.Contains(ready, id) {
				t.Errorf("%s came back into bd ready after reclaim:\n%s", id, ready)
			}
			show := bdIn(t, dir, "show", id)
			if !strings.Contains(show, "PR #62") {
				t.Errorf("close reason does not name the PR; the next reader cannot tell why it closed:\n%s", show)
			}

			// And running it again is a no-op — the reconciler must be safe to
			// call before every allocation.
			again, err := ReconcileBoard(repo, bdClient{dir: dir, run: execBD}, merged, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(again) != 0 {
				t.Errorf("second reconcile acted again: %+v", again)
			}
		})
	}
}

// A bead whose PR is open, not merged, is exactly the review-queue state the
// skill leaves behind. It must stay where it is.
func TestConformanceOpenPRDoesNotCloseTheBead(t *testing.T) {
	dir := scratchRepo(t)
	id := newBead(t, dir, "work in review")
	bdIn(t, dir, "update", id, "--claim")
	got, err := ReconcileBoard(Repo{Name: "scratch", Path: dir, DefaultBranch: "main"}, bdClient{dir: dir, run: execBD},
		func(Repo) ([]PR, error) {
			return []PR{{Number: 70, State: "OPEN", HeadRefName: "bead/" + id, BaseRefName: "main"}}, nil
		}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("reconcile touched a bead in review: %+v", got)
	}
	if show := bdIn(t, dir, "show", id, "--json"); !strings.Contains(show, `"in_progress"`) {
		t.Errorf("bead left in_progress:\n%s", show)
	}
}

// fw-ojk's acceptance criterion against a real bd database: the bead whose PR
// merged into a feature branch is still there afterwards. This is the case that
// shipped — spot-2ig closed on a PR that went into fleet/builder-permissions —
// and the property that matters is that the bead survives, not that a function
// returned a particular string.
func TestConformanceAPRMergedIntoAFeatureBranchLeavesTheBeadOpen(t *testing.T) {
	dir := scratchRepo(t)
	id := newBead(t, dir, "work whose PR merged into the wrong branch")

	got, err := ReconcileBoard(Repo{Name: "scratch", Path: dir, DefaultBranch: "main"}, bdClient{dir: dir, run: execBD},
		func(Repo) ([]PR, error) {
			return []PR{{Number: 4, State: "MERGED", Title: "the work, merged into a feature branch",
				HeadRefName: "bead/" + id, BaseRefName: "fleet/builder-permissions"}}, nil
		}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != "merged-elsewhere" {
		t.Fatalf("reconcile = %+v, want %s reported as merged-elsewhere", got, id)
	}
	if !strings.Contains(got[0].Detail, "fleet/builder-permissions") {
		t.Errorf("report does not name the branch the work went to: %q", got[0].Detail)
	}
	if show := bdIn(t, dir, "show", id, "--json"); strings.Contains(show, `"closed"`) {
		t.Errorf("%s was closed while main does not have the work:\n%s", id, show)
	}
	// And it is still dispatchable: the work is not done, so the coordinator
	// must still be able to see it.
	if ready := bdIn(t, dir, "ready", "--json"); !strings.Contains(ready, id) {
		t.Errorf("%s left bd ready without its work reaching main:\n%s", id, ready)
	}
}
