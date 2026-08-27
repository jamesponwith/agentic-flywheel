package main

import (
	"testing"
)

// A builder's evidence is what IT committed, not what its base already
// carried. Counting against main credited the base branch's commits and would
// have called a second no-op green — for a different reason than the first.
func TestCommitsSinceIgnoresTheBase(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := gitCmd(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@invalid")
	run("config", "user.name", "t")
	run("commit", "-q", "--allow-empty", "-m", "root")
	run("checkout", "-q", "-b", "feature")
	run("commit", "-q", "--allow-empty", "-m", "base branch work")

	base := headOf(dir)
	run("checkout", "-q", "-b", "bead/x-1")

	if got := commitsSince(dir, base, "bead/x-1"); got != 0 {
		t.Errorf("a builder that committed nothing counted %d — that is a false green", got)
	}
	run("commit", "-q", "--allow-empty", "-m", "the builder's actual work")
	if got := commitsSince(dir, base, "bead/x-1"); got != 1 {
		t.Errorf("counted %d, want 1 — only the builder's own commit", got)
	}
}

func TestCommitsSinceHandlesAMissingBase(t *testing.T) {
	// If HEAD could not be read, claim no evidence rather than guessing.
	if got := commitsSince(t.TempDir(), "", "any"); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}
