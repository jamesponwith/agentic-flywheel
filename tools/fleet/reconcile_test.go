package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func reconcileRepo(t *testing.T) Repo {
	t.Helper()
	dir := t.TempDir()
	for _, a := range [][]string{{"init", "-q", "-b", "main"},
		{"config", "user.email", "r@r"}, {"config", "user.name", "r"}} {
		c := exec.Command("git", a...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	c := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "root")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return Repo{Name: "r", Path: dir}
}

func addWorktree(t *testing.T, repo Repo, bead string, commits int) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt-"+bead)
	if err := git2(repo.Path, "worktree", "add", "-b", "bead/"+bead, wt); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < commits; i++ {
		c := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "work")
		c.Dir = wt
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	return wt
}

// A leftover with commits is somebody's work. Destroying unmerged commits to
// tidy up is worse than the mess it cleans.
func TestReconcileNeverDestroysCommits(t *testing.T) {
	repo := reconcileRepo(t)
	wt := addWorktree(t, repo, "keep-1", 2)

	got, err := Reconcile(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != "kept" {
		t.Fatalf("got %+v, want one kept leftover", got)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("worktree not detached — the ground stays occupied")
	}
	out, err := inDir(repo.Path, "git", "rev-parse", "--verify", "bead/keep-1").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Error("branch with commits was deleted; that is unmerged work destroyed")
	}
}

// A leftover with nothing in it blocks its bead forever, because
// `git worktree add -b bead/<id>` is fatal when the branch exists.
func TestReconcileSweepsEmptyLeftovers(t *testing.T) {
	repo := reconcileRepo(t)
	addWorktree(t, repo, "sweep-1", 0)

	got, err := Reconcile(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != "swept" {
		t.Fatalf("got %+v, want one swept leftover", got)
	}
	if _, err := inDir(repo.Path, "git", "rev-parse", "--verify", "bead/sweep-1").Output(); err == nil {
		t.Error("empty branch survived; the bead stays unreachable to the coordinator")
	}
	// And the bead must now be workable again.
	wt2 := filepath.Join(t.TempDir(), "again")
	if err := git2(repo.Path, "worktree", "add", "-b", "bead/sweep-1", wt2); err != nil {
		t.Errorf("bead still unreachable after sweep: %v", err)
	}
}

// A human's worktree is not a builder's leftover.
func TestReconcileIgnoresNonBeadWorktrees(t *testing.T) {
	repo := reconcileRepo(t)
	wt := filepath.Join(t.TempDir(), "human")
	if err := git2(repo.Path, "worktree", "add", "-b", "my-feature", wt); err != nil {
		t.Fatal(err)
	}
	got, err := Reconcile(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("touched a human's worktree: %+v", got)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Error("human worktree removed")
	}
}
