package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// beadRepo builds a repo with one commit on main and returns its path.
func beadRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "d@invalid"}, {"config", "user.name", "d"},
	} {
		if out, err := inDir(dir, "git", a...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"add", "-A"}, {"commit", "-qm", "seed"}} {
		if out, err := inDir(dir, "git", a...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	return dir
}

func branchExists(t *testing.T, dir, branch string) bool {
	t.Helper()
	return inDir(dir, "git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

func TestOneNoOpDoesNotLockTheBeadForever(t *testing.T) {
	// A builder that commits nothing still leaves bead/<id> behind, and the
	// next run's `worktree add -b` fails with "already exists" — so the bead
	// can never be attempted again. That is how fw-ax2 blocked its own retry
	// at $0 and 0s, with the queue quietly losing one bead.
	dir := beadRepo(t)
	base := headOf(dir)
	if out, err := inDir(dir, "git", "branch", "bead/x").CombinedOutput(); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if err := clearEmptyBranch(dir, base, "bead/x"); err != nil {
		t.Fatalf("refused to clear an empty leftover: %v", err)
	}
	if branchExists(t, dir, "bead/x") {
		t.Error("empty leftover survived; the next worktree add still fails")
	}
}

// gitAll runs each command in dir and fails the test on the first error.
func gitAll(t *testing.T, dir string, cmds ...[]string) {
	t.Helper()
	for _, a := range cmds {
		if out, err := inDir(dir, "git", a...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

func TestABranchWithWorkIsNeverDeleted(t *testing.T) {
	// The important half. Losing an agent's commits to a tidy-up is far worse
	// than a stuck bead, so this must refuse rather than guess — and the
	// refusal names the size of the work in files, which a rewrite cannot
	// inflate the way it inflates a commit count.
	dir := beadRepo(t)
	base := headOf(dir)
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAll(t, dir,
		[]string{"checkout", "-q", "-b", "bead/y"},
		[]string{"add", "-A"},
		[]string{"commit", "-qm", "real work"},
		[]string{"checkout", "-q", "main"},
	)
	err := clearEmptyBranch(dir, base, "bead/y")
	if err == nil {
		t.Fatal("deleted a branch carrying a file change")
	}
	for _, want := range []string{"refusing", "1 file(s) changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
	if !branchExists(t, dir, "bead/y") {
		t.Fatal("the work is gone")
	}
}

func TestAStaleBranchFromBeforeARewriteIsStillEmpty(t *testing.T) {
	// fw-web: bead/fw-ax2 carried zero commits of its own, but after main's
	// history was force-pushed with new SHAs every one of its ancestors read
	// as "unmerged", and the branch could never be swept again. The rewrite
	// preserved every tree, so content — not SHAs — is what to compare.
	dir := beadRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "g.txt"), []byte("pre"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAll(t, dir,
		[]string{"add", "-A"},
		[]string{"commit", "-qm", "pre-rewrite tip"},
		[]string{"branch", "bead/x"},                               // a no-op builder left this at the old tip
		[]string{"commit", "-q", "--amend", "-m", "rewritten tip"}, // same tree, new SHA
	)
	base := headOf(dir)
	if n := commitsSince(dir, base, "bead/x"); n != 1 {
		t.Fatalf("setup: expected the old tip to read as 1 unmerged commit, got %d", n)
	}
	if err := clearEmptyBranch(dir, base, "bead/x"); err != nil {
		t.Fatalf("refused a branch that added nothing: %v", err)
	}
	if branchExists(t, dir, "bead/x") {
		t.Error("stale pre-rewrite leftover survived; the bead is locked forever")
	}
}

func TestAStaleBranchWithWorkIsRefusedAfterARewrite(t *testing.T) {
	// The other half of the rewrite case: an old-SHA branch that also carries
	// a real change must still be refused, and for the change, not the SHAs.
	dir := beadRepo(t)
	gitAll(t, dir,
		[]string{"checkout", "-q", "-b", "bead/w"},
		[]string{"checkout", "-q", "main"},
		[]string{"commit", "-q", "--amend", "-m", "rewritten seed"},
	)
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAll(t, dir,
		[]string{"checkout", "-q", "bead/w"},
		[]string{"add", "-A"},
		[]string{"commit", "-qm", "real work on old history"},
		[]string{"checkout", "-q", "main"},
	)
	err := clearEmptyBranch(dir, headOf(dir), "bead/w")
	if err == nil || !strings.Contains(err.Error(), "shares no history") {
		// Amending the root leaves no common ancestor at all, which is the
		// fail-closed path: no merge-base, no guess.
		t.Fatalf("expected a refusal for unrelated history, got: %v", err)
	}
	if !branchExists(t, dir, "bead/w") {
		t.Fatal("the work is gone")
	}
}

func TestDeletingWhatMainStillHasIsWork(t *testing.T) {
	// A branch whose tree matches an OLDER main commit is not empty: it undid
	// something main kept. Only trees from the merge-base forward count.
	dir := beadRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "g.txt"), []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAll(t, dir,
		[]string{"add", "-A"},
		[]string{"commit", "-qm", "add g"},
		[]string{"checkout", "-q", "-b", "bead/v"},
		[]string{"rm", "-q", "g.txt"},
		[]string{"commit", "-qm", "delete g"}, // tree now equals the seed commit's
		[]string{"checkout", "-q", "main"},
	)
	err := clearEmptyBranch(dir, headOf(dir), "bead/v")
	if err == nil || !strings.Contains(err.Error(), "1 file(s) changed") {
		t.Fatalf("expected a refusal naming the deleted file, got: %v", err)
	}
	if !branchExists(t, dir, "bead/v") {
		t.Fatal("the work is gone")
	}
}

func TestALiveBuildersBranchIsLeftAlone(t *testing.T) {
	// A branch checked out in another worktree is a builder that is still
	// running, not a leftover. Deleting it mid-run would be the worst outcome
	// of the three.
	dir := beadRepo(t)
	base := headOf(dir)
	wt := filepath.Join(t.TempDir(), "live")
	if out, err := inDir(dir, "git", "worktree", "add", "-q", "-b", "bead/z", wt).CombinedOutput(); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	err := clearEmptyBranch(dir, base, "bead/z")
	if err == nil {
		t.Fatal("cleared a branch that a running builder holds")
	}
	if !strings.Contains(err.Error(), "another worktree") {
		t.Errorf("wrong reason: %v", err)
	}
	if !branchExists(t, dir, "bead/z") {
		t.Error("deleted a live builder's branch")
	}
}

func TestNoBranchIsNotAnError(t *testing.T) {
	dir := beadRepo(t)
	if err := clearEmptyBranch(dir, headOf(dir), "bead/never-existed"); err != nil {
		t.Errorf("errored on the ordinary case: %v", err)
	}
}
