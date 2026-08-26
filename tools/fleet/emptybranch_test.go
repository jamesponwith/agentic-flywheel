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

func TestABranchWithWorkIsNeverDeleted(t *testing.T) {
	// The important half. Losing an agent's commits to a tidy-up is far worse
	// than a stuck bead, so this must refuse rather than guess.
	dir := beadRepo(t)
	base := headOf(dir)
	for _, a := range [][]string{
		{"checkout", "-q", "-b", "bead/y"},
		{"commit", "-q", "--allow-empty", "-m", "real work"},
		{"checkout", "-q", "main"},
	} {
		if out, err := inDir(dir, "git", a...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	err := clearEmptyBranch(dir, base, "bead/y")
	if err == nil {
		t.Fatal("deleted a branch carrying commits")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("refused for an unclear reason: %v", err)
	}
	if !branchExists(t, dir, "bead/y") {
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
