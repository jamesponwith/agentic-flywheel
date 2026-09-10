package main

import (
	"strings"
	"testing"
)

func TestManifestRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if _, ok := readManifest(dir); ok {
		t.Fatal("read a manifest that was never written")
	}
	want := runManifest{Bead: "x-1", Branch: "bead/x-1", Agent: "repo/builder", Started: base}
	if err := writeManifest(dir, want); err != nil {
		t.Fatal(err)
	}
	got, ok := readManifest(dir)
	if !ok || got.Bead != want.Bead || got.Branch != want.Branch || got.Agent != want.Agent {
		t.Fatalf("read back %+v, want %+v", got, want)
	}
	if err := clearManifest(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok := readManifest(dir); ok {
		t.Fatal("manifest survived clearManifest")
	}
	// Clearing an already-clear manifest must not be an error — build()'s
	// defer runs unconditionally, kill or no kill.
	if err := clearManifest(dir); err != nil {
		t.Fatalf("clearing a missing manifest: %v", err)
	}
}

// DRILL: the coordinator itself is killed mid-flight, not the builder it
// spawned — the case fw-lb8.7 does not cover. Its manifest survives on disk
// because the defer that clears it never ran. The bead must be dispatchable
// again on the next run, without a human, but ONLY when nothing was
// committed.
func TestReleaseStrandedReleasesAnEmptyBranch(t *testing.T) {
	repo := reconcileRepo(t)
	addWorktree(t, repo, "tf4-1", 0) // claimed, then the coordinator died: no commits

	f := newFake(Bead{ID: "tf4-1", Status: "in_progress", Metadata: map[string]string{
		leaseHolderKey: "should", leaseExpiresKey: "be-cleared-too",
	}})
	if err := writeManifest(repo.Path, runManifest{Bead: "tf4-1", Branch: "bead/tf4-1", Agent: "repo/builder", Started: base}); err != nil {
		t.Fatal(err)
	}

	got, err := ReleaseStranded(repo, bdClient{dir: repo.Path, run: f.run})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Bead != "tf4-1" {
		t.Fatalf("released %+v, want tf4-1", got)
	}
	if f.beads["tf4-1"].Status != "open" {
		t.Error("bead not returned to the queue — the fleet cannot dispatch it again")
	}
	if !strings.Contains(bdReadyIDs(f), "tf4-1") {
		t.Error("bead is not in the ready queue after release")
	}
	if _, ok := f.beads["tf4-1"].Metadata[leaseHolderKey]; ok {
		t.Error("stale lease metadata not cleared")
	}
	var noted bool
	for _, c := range f.calls {
		if strings.HasPrefix(c, "note tf4-1") && strings.Contains(c, "killed outright") {
			noted = true
		}
	}
	if !noted {
		t.Error("no note explaining why the bead came back — the next agent needs to know")
	}
	if _, ok := readManifest(repo.Path); ok {
		t.Error("manifest not cleared after release — the next run would recheck it forever")
	}
}

// A branch with commits is somebody's work, maybe already pushed and under
// review. ReleaseStranded must never reopen a bead over it, exactly as
// Reconcile and clearEmptyBranch refuse to delete one.
func TestReleaseStrandedNeverTouchesABranchWithCommits(t *testing.T) {
	repo := reconcileRepo(t)
	addWorktree(t, repo, "tf4-2", 3)

	f := newFake(Bead{ID: "tf4-2", Status: "in_progress"})
	if err := writeManifest(repo.Path, runManifest{Bead: "tf4-2", Branch: "bead/tf4-2", Agent: "repo/builder"}); err != nil {
		t.Fatal(err)
	}

	got, err := ReleaseStranded(repo, bdClient{dir: repo.Path, run: f.run})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("released a branch carrying commits: %+v", got)
	}
	if f.beads["tf4-2"].Status != "in_progress" {
		t.Error("a bead with real work was reopened")
	}
}

// A bead a human (or a reconcile-board) already closed must never be
// reopened just because a stale manifest still names it.
func TestReleaseStrandedLeavesAClosedBeadClosed(t *testing.T) {
	repo := reconcileRepo(t)
	addWorktree(t, repo, "tf4-3", 0)

	f := newFake(Bead{ID: "tf4-3", Status: "closed"})
	if err := writeManifest(repo.Path, runManifest{Bead: "tf4-3", Branch: "bead/tf4-3", Agent: "repo/builder"}); err != nil {
		t.Fatal(err)
	}

	got, err := ReleaseStranded(repo, bdClient{dir: repo.Path, run: f.run})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("released a closed bead: %+v", got)
	}
	if f.beads["tf4-3"].Status != "closed" {
		t.Error("a closed bead was reopened")
	}
	if _, ok := readManifest(repo.Path); ok {
		t.Error("stale manifest for a closed bead was not cleared")
	}
}

// A transient bd failure is not evidence the bead was resolved. Clearing the
// manifest on it would erase the only record of the strand over an infra
// hiccup, recreating fw-tf4 through a different door — so the manifest must
// survive for the next run to try again.
func TestReleaseStrandedKeepsTheManifestOnABDFailure(t *testing.T) {
	repo := reconcileRepo(t)
	addWorktree(t, repo, "tf4-4", 0)

	// No bead registered in the fake: bd.show fails exactly as it would if
	// bd itself were unreachable for a moment.
	f := newFake()
	if err := writeManifest(repo.Path, runManifest{Bead: "tf4-4", Branch: "bead/tf4-4", Agent: "repo/builder"}); err != nil {
		t.Fatal(err)
	}

	got, err := ReleaseStranded(repo, bdClient{dir: repo.Path, run: f.run})
	if err == nil {
		t.Fatal("want an error from a failing bd.show, got nil")
	}
	if got != nil {
		t.Fatalf("released %+v on a bd failure", got)
	}
	if _, ok := readManifest(repo.Path); !ok {
		t.Error("manifest cleared on a transient bd failure — the strand can never be retried")
	}
}

// No manifest means no killed run to recover from — the common case, every
// clean run.
func TestReleaseStrandedNoManifestIsANoop(t *testing.T) {
	repo := reconcileRepo(t)
	f := newFake()
	got, err := ReleaseStranded(repo, bdClient{dir: repo.Path, run: f.run})
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v; want nil, nil", got, err)
	}
	if len(f.calls) != 0 {
		t.Errorf("touched bd with no manifest present: %v", f.calls)
	}
}

func bdReadyIDs(f *fakeBD) string {
	out, err := f.run(".", "ready", "--json")
	if err != nil {
		panic(err)
	}
	return string(out)
}
