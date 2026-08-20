// The tests that matter here are the ones where the probe FAILS. A probe that
// only passes against a working guard is indistinguishable from `return true`,
// and `return true` is what the three shipped-and-did-nothing versions of
// push-guard.sh effectively were.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hookOf wraps the repo's real push-guard.sh in a pre-push hook, the way
// lefthook does. Testing against the real script and not a stand-in is the
// point: a fixture guard would pass a probe the shipped one failed.
func hookOf(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "flywheel", "push-guard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("push-guard.sh is not where this repo keeps it: %v", err)
	}
	return "#!/usr/bin/env bash\nexec " + p + " \"$@\"\n"
}

// hookRepo is a git repo with the given content installed as its pre-push hook.
// An empty hook means none installed at all.
func hookRepo(t *testing.T, hook string, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range [][]string{{"init", "-q", "-b", "main"},
		{"config", "user.email", "d@d"}, {"config", "user.name", "d"}} {
		if out, err := inDir(dir, "git", a...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	if hook != "" {
		writeHook(t, dir, hook, mode)
	}
	return dir
}

func writeHook(t *testing.T, repo, hook string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(repo, ".git", "hooks", "pre-push")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(hook), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil { // WriteFile honours umask; this does not
		t.Fatal(err)
	}
}

func TestProbeGuard(t *testing.T) {
	real := hookOf(t)
	tests := []struct {
		name string
		hook string
		mode os.FileMode
		want bool
		// wantWhy is a substring: the reason has to be actionable, not just
		// present. "It failed" sends the reader to run the hook by hand.
		wantWhy string
	}{
		{
			name: "the real guard refuses main and allows a bead branch",
			hook: real, mode: 0o755, want: true,
			wantWhy: "refuses main",
		},
		{
			// The hook was never installed — install-hooks.sh has not been run.
			name: "removed", hook: "", mode: 0, want: false,
			wantWhy: "install-hooks.sh",
		},
		{
			// lefthook's `commands` filter judges the FILES being pushed and
			// skips a command with none. Sensible for a linter, wrong for a rule
			// about refs: it printed this and let a builder push straight to main.
			name: "neutered by lefthook's file filter",
			hook: "#!/bin/sh\necho '(skip) no matching push files'\nexit 0\n",
			mode: 0o755, want: false,
			wantWhy: "let an agent push main",
		},
		{
			// `skip_empty: false` is not a key in lefthook 1.13.6. The config
			// still parses, the hook still runs, and it decides nothing.
			name: "neutered to a no-op",
			hook: "#!/bin/sh\nexit 0\n",
			mode: 0o755, want: false,
			wantWhy: "let an agent push main",
		},
		{
			// git skips a hook it cannot execute, silently and without a word.
			name: "present, wired, and not executable",
			hook: real, mode: 0o644, want: false,
			wantWhy: "not executable",
		},
		{
			// The other direction, and the one a one-sided probe would bless: a
			// hook that refuses everything passes "does it refuse main?" while
			// stranding every builder in the fleet on its own bead branch.
			name: "refuses everything, judges nothing",
			hook: "#!/bin/sh\nexit 1\n",
			mode: 0o755, want: false,
			wantWhy: "not judging",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, why := probeGuard(hookRepo(t, tt.hook, tt.mode))
			if ok != tt.want {
				t.Errorf("probeGuard ok = %v, want %v (why: %s)", ok, tt.want, why)
			}
			if !strings.Contains(why, tt.wantWhy) {
				t.Errorf("why = %q, want it to mention %q", why, tt.wantWhy)
			}
		})
	}
}

// The probe asks a hook for a verdict. A hook that never answers is not a
// verdict, and waiting for it would hang the whole roster on one repo.
//
// The hook forks rather than execs, on purpose. Cancelling the context kills
// the shell and not the `sleep` it started, and CombinedOutput goes on waiting
// for the pipe that orphan still holds — so the first version of this probe
// had a 20s deadline and blocked for ten minutes. Nothing but the fork catches
// that; a hook that sleeps in its own process exits on the kill and looks fine.
func TestProbeGuardDoesNotWaitForever(t *testing.T) {
	repo := hookRepo(t, "#!/bin/sh\nsleep 600 &\nwait\n", 0o755)
	start := time.Now()
	ok, why := probeGuardWithin(repo, 200*time.Millisecond)
	if ok {
		t.Errorf("a hook that never answers was read as a working guard: %s", why)
	}
	if !strings.Contains(why, "no verdict") {
		t.Errorf("why = %q, want it to name the timeout", why)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("waited %s on a 200ms deadline — the orphan is still holding the pipe", elapsed)
	}
}

// The acceptance criterion with teeth. The probe drives the machinery git uses
// to push, so the thing to prove is that it stops short of pushing.
func TestProbeGuardMovesNoRef(t *testing.T) {
	bare := t.TempDir()
	if out, err := inDir(bare, "git", "init", "-q", "--bare", ".").CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	// Seed the remote BEFORE the hook exists, so this setup push has no guard
	// to bypass — the test must never need --no-verify to arrange itself.
	repo := hookRepo(t, "", 0)
	if err := writeFile(filepath.Join(repo, "f.txt"), "x"); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{
		{"add", "-A"}, {"commit", "-qm", "seed"},
		{"remote", "add", "origin", bare}, {"push", "-q", "origin", "main"},
	} {
		if out, err := inDir(repo, "git", a...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	writeHook(t, repo, hookOf(t), 0o755)

	before := refsOf(t, bare)
	localBefore := refsOf(t, repo)
	if before == "" {
		t.Fatal("the remote has no refs to move; the fixture proves nothing")
	}

	if ok, why := probeGuard(repo); !ok {
		t.Fatalf("the real guard failed its own probe: %s", why)
	}

	if got := refsOf(t, bare); got != before {
		t.Errorf("the probe moved a ref on the remote:\nbefore %q\nafter  %q", before, got)
	}
	if got := refsOf(t, repo); got != localBefore {
		t.Errorf("the probe moved a local ref:\nbefore %q\nafter  %q", localBefore, got)
	}
}

func refsOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := inDir(dir, "git", "for-each-ref", "--format=%(refname) %(objectname)").Output()
	if err != nil {
		t.Fatalf("for-each-ref in %s: %v", dir, err)
	}
	return string(out)
}

// fullRepo has every artifact the manifest asks of a Go instance, so Missing is
// empty and the headline is decided by the probe alone.
func fullRepo(t *testing.T) Repo {
	t.Helper()
	var paths []string
	for _, a := range Manifest {
		if a.Lang != "" && a.Lang != "go" {
			continue
		}
		paths = append(paths, a.Path)
	}
	return repoWith(t, paths...)
}

// A repo at full artifact parity with a dead guard must not read as complete.
// This is the same failure as the untracked-file one that made three repos
// claim parity they did not have, one rung further up.
func TestDiagnoseWillNotSayCompleteOverADeadGuard(t *testing.T) {
	t.Setenv("CI", "") // probes are skipped under CI; this test is the probe

	r := fullRepo(t)
	writeHook(t, r.Path, "#!/bin/sh\nexit 0\n", 0o755)
	d := Diagnose(r)

	if len(d.Missing) != 0 {
		t.Fatalf("fixture is not at artifact parity, so this proves nothing: %v", d.Missing)
	}
	if len(d.Failing()) != 1 {
		t.Fatalf("failing probes = %v, want the push guard", d.Probes)
	}
	if s := d.String(); strings.Contains(s, "complete (") {
		t.Errorf("reported complete over a guard that refuses nothing:\n%s", s)
	}
	if s := d.String(); !strings.Contains(s, "push guard") {
		t.Errorf("did not name the failing check:\n%s", s)
	}
}

func TestDiagnoseSaysCompleteWhenTheGuardFires(t *testing.T) {
	t.Setenv("CI", "")

	r := fullRepo(t)
	writeHook(t, r.Path, hookOf(t), 0o755)
	d := Diagnose(r)

	if len(d.Failing()) != 0 {
		t.Fatalf("the real guard failed its own probe: %v", d.Failing())
	}
	// drift.yml greps this literal to decide whether to file an issue.
	if s := d.String(); !strings.Contains(s, "complete (") {
		t.Errorf("a repo at parity with a working guard is not complete:\n%s", s)
	}
}

// Hooks are not installed in a CI checkout and never run there. Probing them
// would report a gap the environment cannot have, and drift.yml would file the
// same issue every Monday forever — the permanent false gap fw-fsa.8 is about.
func TestProbesAreSkippedWhereHooksCannotRun(t *testing.T) {
	t.Setenv("CI", "true")
	if got := Diagnose(fullRepo(t)); len(got.Probes) != 0 {
		t.Errorf("probed under CI: %v", got.Probes)
	}
	if s := Diagnose(fullRepo(t)).String(); !strings.Contains(s, "complete (") {
		t.Errorf("a CI checkout at parity must still read as parity:\n%s", s)
	}

	t.Setenv("CI", "")
	r := fullRepo(t)
	r.SkipStages = []string{"agents"}
	if got := Diagnose(r); len(got.Probes) != 0 {
		t.Errorf("probed a repo that declared no use for the agents stage: %v", got.Probes)
	}
}

// core.hooksPath moves the hook git runs. Probing .git/hooks anyway would read
// a file git ignores — "looks installed" in its purest form (ADR 0012).
func TestProbeGuardFollowsCoreHooksPath(t *testing.T) {
	t.Setenv("CI", "")

	repo := hookRepo(t, "#!/bin/sh\nexit 0\n", 0o755) // a dead hook where git is NOT looking
	elsewhere := filepath.Join(repo, "githooks")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "pre-push"), []byte(hookOf(t)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(elsewhere, "pre-push"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := inDir(repo, "git", "config", "core.hooksPath", "githooks").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v\n%s", err, out)
	}

	if ok, why := probeGuard(repo); !ok {
		t.Errorf("read the ignored .git/hooks copy instead of the live one: %s", why)
	}
}
