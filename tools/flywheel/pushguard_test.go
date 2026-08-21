// Tests for push-guard.sh, the agent-scoped replacement for a branch ruleset.
//
// The refusals matter, but the permission matters more: a guard that also
// blocks the maintainer is the deadlock that got the ruleset deleted, and it
// would be deleted again.
package flywheel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pushLine is what git writes to a pre-push hook's stdin.
func pushLine(remoteRef string) string {
	return "refs/heads/x deadbeef " + remoteRef + " 0000000000000000000000000000000000000000\n"
}

// runPushGuardIn drives the guard inside dir, which must be a git repository
// the test created.
//
// The directory is chosen, never inherited, for two reasons the suite got
// wrong. A linked worktree is one of the guard's two agent signals, so a test
// that ran wherever it happened to live passed in the main checkout and failed
// in every builder worktree — a gate red for every agent, and only for agents
// (fw-4zb). And a real refusal appends to $REPO/.flywheel/agent-log.jsonl, so
// the same runs were writing push.refused records into the repo's own audit
// log: fabricated evidence of refusals that never happened, in a system whose
// argument is that refusals are evidence.
func runPushGuardIn(t *testing.T, dir, stdin string, env ...string) (string, int) {
	t.Helper()
	script, err := filepath.Abs("push-guard.sh")
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command("bash", script)
	c.Dir = dir
	c.Env = append(append(hermeticEnv(), "FLYWHEEL_HOME="+filepath.Join(dir, "home")), env...)
	c.Stdin = strings.NewReader(stdin)
	out, err := c.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running push-guard.sh: %v\n%s", err, out)
	}
	return string(out), code
}

func runPushGuard(t *testing.T, stdin string, env ...string) (string, int) {
	t.Helper()
	return runPushGuardIn(t, scratch(t), stdin, env...)
}

func TestPushGuardStopsAgents(t *testing.T) {
	cases := []struct {
		name, ref, want string
	}{
		{"main", "refs/heads/main", "may not push to 'main'"},
		{"master", "refs/heads/master", "may not push to 'master'"},
		{"a tag is a release", "refs/tags/v1.0.0", "may not push tags"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runPushGuard(t, pushLine(tc.ref), "FLYWHEEL_AGENT=fleet/builder-go")
			if code == 0 {
				t.Errorf("allowed %s from an agent; ADR 0003 forbids it\n%s", tc.ref, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("refusal does not say why: want %q, got:\n%s", tc.want, out)
			}
		})
	}
}

func TestPushGuardNeverBlocksTheMaintainer(t *testing.T) {
	// The whole point. A ruleset on a solo repo requires a review the only
	// reviewer cannot give, so it got deleted and took ADR 0003's enforcement
	// with it. This guard is allowed to exist only because it stays out of the
	// human's way — no FLYWHEEL_AGENT, not a linked worktree, no opinion.
	out, code := runPushGuard(t, pushLine("refs/heads/main"))
	if code != 0 {
		t.Errorf("blocked a human pushing to main; that is the deadlock, not the guard\n%s", out)
	}
}

func TestPushGuardIsNotFooledByTheCallersDirectory(t *testing.T) {
	// git answers --git-dir absolutely and --git-common-dir relatively when the
	// caller sits in a subdirectory. Comparing the two as strings reported the
	// main repository as a linked worktree, so the guard refused the maintainer
	// every push not made from the root. Run from a subdirectory on purpose —
	// of a repo the test made, so "is this a worktree" has one right answer.
	sub := filepath.Join(scratch(t), "pkg", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runPushGuardIn(t, sub, pushLine("refs/heads/main"))
	if code != 0 {
		t.Errorf("blocked a human pushing from a subdirectory\n%s", out)
	}
}

func TestPushGuardLogsRefusalsWhereItWasPointed(t *testing.T) {
	// A refusal is evidence and must be recorded — into the repo the guard was
	// actually run against. Before fw-4zb the suite recorded them into THIS
	// repo's audit log, which is worse than not recording at all: it is
	// evidence of a refusal that never happened.
	dir := scratch(t)
	if _, code := runPushGuardIn(t, dir, pushLine("refs/heads/main"),
		"FLYWHEEL_AGENT=fleet/builder-go"); code == 0 {
		t.Fatal("allowed an agent to push main")
	}
	lines := readLines(t, filepath.Join(dir, ".flywheel", "agent-log.jsonl"))
	if len(lines) != 1 || !strings.Contains(lines[0], "push.refused") {
		t.Fatalf("refusal not recorded in the scratch repo: %q", lines)
	}
	if !strings.Contains(lines[0], `"refs":"main"`) {
		t.Errorf("record does not name the refused ref: %s", lines[0])
	}
}

func TestPushGuardRecordsWhenInvokedTheWayLefthookInvokesIt(t *testing.T) {
	// The only path on which the guard ever runs for real. It recorded the
	// refusal when the suite called it directly and recorded nothing under
	// lefthook, which invokes it as .lefthook/pre-push/push-guard.sh — so
	// dirname was .lefthook/pre-push, which holds no guard.sh, and the error
	// went into 2>/dev/null || true. Every push.refused record in the repo
	// came from tests; not one came from a refusal (fw-51q).
	//
	// A symlink does not fix it: bash sets $0 to the path as invoked.
	dir := scratch(t)
	tools := filepath.Join(dir, "tools", "flywheel")
	hookDir := filepath.Join(dir, ".lefthook", "pre-push")
	for _, d := range []string{tools, hookDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"guard.sh", "push-guard.sh"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tools, f), src, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Invoked from a directory that holds no guard.sh, exactly as lefthook does.
	if err := os.Symlink(filepath.Join(tools, "push-guard.sh"),
		filepath.Join(hookDir, "push-guard.sh")); err != nil {
		t.Skip("symlinks unavailable")
	}
	c := exec.Command("bash", filepath.Join(hookDir, "push-guard.sh"))
	c.Dir = dir
	c.Env = append(hermeticEnv(), "FLYWHEEL_AGENT=fleet/builder-go",
		"FLYWHEEL_HOME="+filepath.Join(dir, "home"))
	c.Stdin = strings.NewReader(pushLine("refs/heads/main"))
	out, err := c.CombinedOutput()
	if err == nil {
		t.Fatalf("allowed an agent to push main\n%s", out)
	}
	lines := readLines(t, filepath.Join(dir, ".flywheel", "agent-log.jsonl"))
	if len(lines) != 1 || !strings.Contains(lines[0], "push.refused") {
		t.Fatalf("the guard refused and recorded nothing — a refusal nobody can "+
			"show happened.\nlog: %q\nstderr: %s", lines, out)
	}
}

func TestPushGuardAllowsTheBranchABuilderActuallyUses(t *testing.T) {
	// A builder's normal path is push a bead branch, open a PR. If the guard
	// caught that too it would refuse every run rather than the bad ones.
	out, code := runPushGuard(t, pushLine("refs/heads/bead/fw-c1l"), "FLYWHEEL_AGENT=fleet/builder-go")
	if code != 0 {
		t.Errorf("blocked a builder's own bead branch; the fleet cannot work at all\n%s", out)
	}
}

func TestPushGuardNamesADeleteAsADelete(t *testing.T) {
	// Deleting main is worse than writing it, and the two are one flag apart
	// on the command line. The refusal should not read identically.
	in := "(delete) 0000000000000000000000000000000000000000 refs/heads/main deadbeef\n"
	out, code := runPushGuard(t, in, "FLYWHEEL_AGENT=fleet/builder-go")
	if code == 0 {
		t.Fatalf("allowed an agent to delete main\n%s", out)
	}
	if !strings.Contains(out, "DELETE") {
		t.Errorf("a delete reads like an ordinary push:\n%s", out)
	}
}

func TestPushGuardRefusesEveryBadRefNotJustTheFirst(t *testing.T) {
	// git feeds one line per ref. Returning on the first match would report
	// main and let the tag through in the same push.
	in := pushLine("refs/heads/main") + pushLine("refs/tags/v9")
	out, code := runPushGuard(t, in, "FLYWHEEL_AGENT=fleet/builder-go")
	if code == 0 {
		t.Fatalf("allowed the push\n%s", out)
	}
	if !strings.Contains(out, "'main'") || !strings.Contains(out, "tags") {
		t.Errorf("only reported some of the refused refs:\n%s", out)
	}
}
