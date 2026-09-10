package main

import (
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	return gitRepoOn(t, "main")
}

// gitRepoOn is gitRepo with the default branch named branch instead of main,
// so a test can drive a repo that does not ship from main (fw-64x).
func gitRepoOn(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", branch},
		{"config", "user.email", "t@invalid"}, {"config", "user.name", "t"},
	} {
		if out, err := gitCmd(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	if out, err := gitCmd(dir, "commit", "-q", "--allow-empty", "-m", msg).CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := gitCmd(dir, args...).CombinedOutput(); err != nil {
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

	got, err := DetectBypasses(Repo{Name: "r", Path: dir, DefaultBranch: "main"}, "")
	if err != nil {
		t.Fatal(err)
	}

	var direct []Bypass
	for _, b := range got {
		if b.Kind == "direct-to-default" {
			direct = append(direct, b)
		}
	}
	// "initial" and the hotfix both bypassed; the feature commit did not.
	if len(direct) != 2 {
		t.Fatalf("found %d direct-to-default, want 2: %+v", len(direct), got)
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

// A repo whose default branch is not main used to be invisible to this
// detector: mergedPRCommits and the rev-list walk both hardcoded "main", so
// on a "trunk"-shipping repo neither ref would exist and every call would
// either error or silently see zero history. Same scenario as
// TestDetectBypasses, just on a differently named branch (fw-64x).
func TestDetectBypassesOnNonMainDefaultBranch(t *testing.T) {
	dir := gitRepoOn(t, "trunk")
	commit(t, dir, "initial")

	run(t, dir, "checkout", "-q", "-b", "feature")
	commit(t, dir, "through the gate")
	run(t, dir, "checkout", "-q", "trunk")
	run(t, dir, "merge", "--no-ff", "-q", "-m", "Merge pull request #1", "feature")

	commit(t, dir, "hotfix straight to trunk")

	got, err := DetectBypasses(Repo{Name: "r", Path: dir, DefaultBranch: "trunk"}, "")
	if err != nil {
		t.Fatal(err)
	}

	var direct []Bypass
	for _, b := range got {
		if b.Kind == "direct-to-default" {
			direct = append(direct, b)
		}
	}
	if len(direct) != 2 {
		t.Fatalf("found %d direct-to-default on a trunk-shipping repo, want 2: %+v", len(direct), got)
	}
	for _, b := range direct {
		if b.Detail == "through the gate — never passed the PR gate" {
			t.Error("counted a commit that arrived via a merged PR")
		}
	}
}

// Without a default branch there is no branch to diff against — refuse
// rather than guess, the same way ReconcileBoard refuses (fw-64x).
func TestDetectBypassesRefusesWithoutADefaultBranch(t *testing.T) {
	dir := gitRepo(t)
	commit(t, dir, "initial")
	if _, err := DetectBypasses(Repo{Name: "r", Path: dir}, ""); err == nil {
		t.Fatal("detected bypasses against an unknown default branch")
	}
}

func TestOverBudget(t *testing.T) {
	// Two is tolerated; the third trips the rule.
	two := []Bypass{{Repo: "r", Kind: "direct-to-default"}, {Repo: "r", Kind: "direct-to-default"}}
	if over := OverBudget(two); len(over) != 0 {
		t.Errorf("two bypasses tripped the budget: %v", over)
	}
	three := append(two, Bypass{Repo: "r", Kind: "direct-to-default"})
	over := OverBudget(three)
	if over["r/direct-to-default"] != 3 {
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

	got, err := DetectBypasses(Repo{Name: "r", Path: dir, DefaultBranch: "main"}, "")
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
		t.Error("the genuine direct-to-default commit was not reported")
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
	c := gitCmd(dir, "commit", "-q", "--allow-empty", "-m", msg,
		"--author", author+" <"+email+">")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

func TestBypassCalibration(t *testing.T) {
	// The classes the first live run got wrong. Each of these is on main with
	// no merge commit, and none of them is a skipped gate.
	dir := gitRepo(t)
	commit(t, dir, "initial")
	commit(t, dir, "Add a feature (#7)")                                             // squash-merged PR
	commitAs(t, dir, "github-actions", "bot@invalid", "learn: weekly DORA snapshot") // automation
	commit(t, dir, "bd: close abc-123")                                              // bookkeeping
	commit(t, dir, "chore: bump thing")                                              // bookkeeping
	commit(t, dir, "fix the parser without a PR")                                    // the real thing

	got, err := DetectBypasses(Repo{Name: "r", Path: dir, DefaultBranch: "main"}, "")
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
