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

// guardPath resolves push-guard.sh absolutely, so it can be run from a
// directory that is not this one.
func pushGuardPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("push-guard.sh")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// notAWorktree is an ordinary git repo: no FLYWHEEL_AGENT and no linked
// worktree, which together are what the guard reads as "a human at a terminal".
//
// The tests used to run the guard in whatever directory they happened to sit
// in. Under a builder that is a LINKED WORKTREE, the guard's structural signal
// fires, and the two tests asserting the human case failed — passing for a
// human in the main checkout and failing for every agent, which is how they
// landed green and then blocked the pre-commit gate for the whole fleet
// (fw-pdi). A test that asserts an environment has to build it (fw-u6g).
func notAWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	c := exec.Command("git", "-C", dir, "init", "-q")
	c.Env = hermeticEnv()
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

// aWorktree is a linked worktree of a scratch repo — the other half of the
// signal, built rather than inherited for the same reason.
func aWorktree(t *testing.T) string {
	t.Helper()
	main := notAWorktree(t)
	for _, a := range [][]string{
		{"-C", main, "config", "user.email", "d@d"},
		{"-C", main, "config", "user.name", "d"},
		{"-C", main, "commit", "-q", "--allow-empty", "-m", "seed"},
	} {
		c := exec.Command("git", a...)
		c.Env = hermeticEnv()
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	linked := filepath.Join(t.TempDir(), "wt")
	c := exec.Command("git", "-C", main, "worktree", "add", "-q", "-b", "bead/x", linked)
	c.Env = hermeticEnv()
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return linked
}

func runPushGuard(t *testing.T, stdin string, env ...string) (string, int) {
	t.Helper()
	return runPushGuardIn(t, notAWorktree(t), stdin, env...)
}

func runPushGuardIn(t *testing.T, dir, stdin string, env ...string) (string, int) {
	t.Helper()
	c := exec.Command("bash", pushGuardPath(t))
	c.Dir = dir
	c.Env = append(hermeticEnv(), env...)
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
	// every push not made from the root. Run from a subdirectory on purpose.
	sub := filepath.Join(notAWorktree(t), "tools", "fleet")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runPushGuardIn(t, sub, pushLine("refs/heads/main"))
	if code != 0 {
		t.Errorf("blocked a human pushing from a subdirectory\n%s", out)
	}
}

// The other half of the signal, and the half the two tests above used to be
// accidentally exercising. A builder worktree is refused even with the
// environment stripped — that is the point of reading it structurally.
func TestPushGuardRefusesAWorktreeWithNoAgentSet(t *testing.T) {
	out, code := runPushGuardIn(t, aWorktree(t), pushLine("refs/heads/main"))
	if code == 0 {
		t.Errorf("let a builder worktree push main with FLYWHEEL_AGENT unset\n%s", out)
	}
	if !strings.Contains(out, "worktree") {
		t.Errorf("refusal does not name what it read: %s", out)
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
