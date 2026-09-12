package main

import (
	"os"
	"path/filepath"
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

// fw-vbt: `git worktree add` only populates tracked content — verified
// empirically against a real repo. copyLocalAgentArtifacts is what makes a
// Repo.AgentsUntracked repo's builder see its untracked scaffolding anyway;
// without it every builder regresses to "Unknown command: /flywheel-next".
func TestCopyLocalAgentArtifactsCarriesWhatExists(t *testing.T) {
	repoPath := t.TempDir()
	wt := t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(repoPath, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude/skills/flywheel-next/SKILL.md", "next")
	write("tools/flywheel/guard.sh", "#!/bin/sh\n")
	write(".claude/settings.local.json", `{"permissions":{"allow":["Bash(cargo build:*)"]}}`)
	// Deliberately do not write flywheel-review or flaky.sh, or .flywheel: not
	// every repo carries every artifact, and that must not be an error.

	repo := Repo{Path: repoPath, AgentsUntracked: true}
	if err := copyLocalAgentArtifacts(repo, wt); err != nil {
		t.Fatalf("copyLocalAgentArtifacts: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(wt, ".claude/skills/flywheel-next/SKILL.md"))
	if err != nil || string(got) != "next" {
		t.Errorf("flywheel-next skill was not carried into the worktree: %v %q", err, got)
	}
	if _, err := os.Stat(filepath.Join(wt, "tools/flywheel/guard.sh")); err != nil {
		t.Errorf("guard.sh was not carried into the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".claude/settings.local.json")); err != nil {
		t.Errorf("settings.local.json was not carried into the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".claude/skills/flywheel-review")); err == nil {
		t.Errorf("an artifact the repo never had appeared in the worktree anyway")
	}
}
