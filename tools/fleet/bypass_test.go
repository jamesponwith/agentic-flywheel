package main

import (
	"os/exec"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"}, {"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	c := exec.Command("git", "commit", "-q", "--allow-empty", "-m", msg)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDetectBypasses(t *testing.T) {
	dir := gitRepo(t)
	commit(t, dir, "initial")

	// Work that went through a PR: branch, commit, no-ff merge.
	run(t, dir, "checkout", "-q", "-b", "feature")
	commit(t, dir, "through the gate")
	run(t, dir, "checkout", "-q", "main")
	run(t, dir, "merge", "--no-ff", "-q", "-m", "Merge pull request #1", "feature")

	// Work that skipped it: committed straight onto main.
	commit(t, dir, "hotfix straight to main")

	got, err := DetectBypasses("r", dir, "")
	if err != nil {
		t.Fatal(err)
	}

	var direct []Bypass
	for _, b := range got {
		if b.Kind == "direct-to-main" {
			direct = append(direct, b)
		}
	}
	// "initial" and the hotfix both bypassed; the feature commit did not.
	if len(direct) != 2 {
		t.Fatalf("found %d direct-to-main, want 2: %+v", len(direct), got)
	}
	for _, b := range direct {
		if b.Detail == "" || b.Commit == "" {
			t.Errorf("bypass missing detail or commit: %+v", b)
		}
		if b.Detail == "through the gate — never passed the PR gate" {
			t.Error("counted a commit that arrived via a merged PR")
		}
	}
}

func TestOverBudget(t *testing.T) {
	// Two is tolerated; the third trips the rule.
	two := []Bypass{{Repo: "r", Kind: "direct-to-main"}, {Repo: "r", Kind: "direct-to-main"}}
	if over := OverBudget(two); len(over) != 0 {
		t.Errorf("two bypasses tripped the budget: %v", over)
	}
	three := append(two, Bypass{Repo: "r", Kind: "direct-to-main"})
	over := OverBudget(three)
	if over["r/direct-to-main"] != 3 {
		t.Errorf("three bypasses did not trip the budget: %v", over)
	}
}

func TestSquashMergedPRsAreNotBypasses(t *testing.T) {
	// GitHub's squash merge leaves no merge commit, so without the subject
	// convention every squash-merged PR reads as a direct push. The first live
	// run produced thirteen such false positives.
	dir := gitRepo(t)
	commit(t, dir, "initial")
	commit(t, dir, "Add the thing (#4)")   // squash-merged PR
	commit(t, dir, "hotfix straight main") // genuine bypass

	got, err := DetectBypasses("r", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range got {
		if strings.Contains(b.Detail, "(#4)") {
			t.Errorf("squash-merged PR reported as a bypass: %+v", b)
		}
	}
	var found bool
	for _, b := range got {
		if strings.Contains(b.Detail, "hotfix") {
			found = true
		}
	}
	if !found {
		t.Error("the genuine direct-to-main commit was not reported")
	}
}

func TestSquashPattern(t *testing.T) {
	for _, tt := range []struct {
		subject string
		want    bool
	}{
		{"Add the thing (#4)", true},
		{"Add the thing (#1234)", true},
		{"Merge pull request #1", false}, // handled by the merge-commit path
		{"fix (#4) mid-sentence", false}, // must anchor at the end
		{"plain commit", false},
	} {
		if got := squashMerged.MatchString(tt.subject); got != tt.want {
			t.Errorf("squashMerged(%q) = %v, want %v", tt.subject, got, tt.want)
		}
	}
}

func commitAs(t *testing.T, dir, author, email, msg string) {
	t.Helper()
	c := exec.Command("git", "commit", "-q", "--allow-empty", "-m", msg,
		"--author", author+" <"+email+">")
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

func TestBypassCalibration(t *testing.T) {
	// The classes the first live run got wrong. Each of these is on main with
	// no merge commit, and none of them is a skipped gate.
	dir := gitRepo(t)
	commit(t, dir, "initial")
	commit(t, dir, "Add a feature (#7)")                                        // squash-merged PR
	commitAs(t, dir, "github-actions", "bot@gh", "learn: weekly DORA snapshot") // automation
	commit(t, dir, "bd: close abc-123")                                         // bookkeeping
	commit(t, dir, "chore: bump thing")                                         // bookkeeping
	commit(t, dir, "fix the parser without a PR")                               // the real thing

	got, err := DetectBypasses("r", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, b := range got {
		subjects = append(subjects, b.Detail)
	}
	for _, bad := range []string{"(#7)", "DORA snapshot", "bd: close", "chore:"} {
		for _, s := range subjects {
			if strings.Contains(s, bad) {
				t.Errorf("false positive (%s): %q", bad, s)
			}
		}
	}
	real := 0
	for _, s := range subjects {
		if strings.Contains(s, "fix the parser") {
			real++
		}
	}
	if real != 1 {
		t.Errorf("the genuine bypass was not reported exactly once; got %v", subjects)
	}
}
