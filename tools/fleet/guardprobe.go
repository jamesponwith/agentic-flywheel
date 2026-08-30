// The rung after "tracked by git": installed has to mean "refuses something".
//
// push-guard.sh went in and did nothing three separate times while looking
// installed. A string compare called the main repo a worktree. lefthook's file
// filter skipped it with "(skip) no matching push files". `skip_empty: false`
// is silently not a key in lefthook 1.13.6 — `lefthook dump` shows you it was
// dropped and the run does not. Each time the file was present, executable and
// wired, so every stat-shaped check would have called it installed.
//
// So doctor probes the guard instead of stat-ing it: feed a synthetic pre-push
// line through the hook git would actually run, and require a refusal.
//
// Two halves, because either alone is passable by a hook that is merely broken.
// Refusing main proves nothing if the same hook also refuses every bead branch
// — that is a crash, not a judgement, and it would strand the fleet rather than
// guard it. The probe demands one refusal AND one permission.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Probe is a behavioural check on an artifact that already exists.
//
// Kept apart from Finding because the two answer different questions: Missing
// is "a clone would not get this", Probes is "this is here and does not act".
// Mixing them would also feed a probe into `doctor -fix`, which copies files.
type Probe struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Why  string `json:"why"`
}

const (
	// probeAgent identifies the probe to the guard, and to the audit log the
	// guard writes on refusal: a passing probe appends one push.refused record
	// naming this agent. That entry is evidence the probe ran, not a real push,
	// and the name is what tells the two apart when reading the log later.
	probeAgent = "fleet/doctor-probe"

	// probeTimeout bounds one hook run. A pre-push hook is a judge and answers
	// in milliseconds; ADR 0013 moved the eight-minute reviewer out of this path
	// precisely because it did not. Something still running after this is not
	// judging anything, and doctor must not hang a nightly waiting for it.
	probeTimeout = 20 * time.Second

	// waitDelay is how long a killed hook gets to release the output pipe
	// before it is taken from it. See runPrePush.
	waitDelay = 2 * time.Second

	// A remote git could never resolve. push-guard.sh ignores its arguments, and
	// a hook that does not cannot reach anything real through this one.
	probeRemote = "probe://fleet-doctor"

	probeLocalOID = "1111111111111111111111111111111111111111"
	zeroOID       = "0000000000000000000000000000000000000000"
)

// probes runs the behavioural checks for a repo. Each probes an artifact that
// present() already vouches for; absence is the manifest's finding, not
// theirs. Skipped entirely where the repo has declared it has no use for the
// agents stage — a template before rollout has no guard to fire.
func probes(repo Repo) []Probe {
	if repo.skips("agents") {
		return nil
	}
	var out []Probe
	// The hook probe is skipped under CI: hooks are not installed in a fresh
	// checkout and never run there, so probing them reports a gap the
	// environment cannot have. That is the permanent false gap that trains
	// people to ignore a checklist (fw-fsa.8), and in drift.yml it would file
	// the same issue every Monday forever.
	if os.Getenv("CI") == "" {
		ok, why := probeGuard(repo.Path)
		out = append(out, Probe{Name: "push guard", OK: ok, Why: why})
	}
	// settings.json is tracked and read from the checkout, so a CI clone sees
	// exactly what a spawned builder sees — this probe holds where the hook
	// one cannot, and drift.yml is where a powerless file should surface.
	// Gated on present(): a gitignored one is the manifest's gap, and reading
	// it off disk anyway would call it working (the second of fw-7al's three).
	if present(repo.Path, settingsPath) {
		ok, why := probeSettings(repo.Path, repo.Lang)
		out = append(out, Probe{Name: "settings grants", OK: ok, Why: why})
	}
	return out
}

// probeGuard reports whether repoPath's installed pre-push hook enforces
// ADR 0003: it must refuse an agent pushing main, and let the same agent push
// its own bead branch. It never pushes anything — the hook is a judge, and this
// asks for the judgement without the push.
func probeGuard(repoPath string) (bool, string) {
	return probeGuardWithin(repoPath, probeTimeout)
}

// probeGuardWithin is probeGuard with the deadline passed in, so the test for
// a hook that never answers does not have to wait out the real one.
func probeGuardWithin(repoPath string, timeout time.Duration) (bool, string) {
	hook, err := prePushHook(repoPath)
	if err != nil {
		return false, "cannot resolve the hooks directory: " + err.Error()
	}
	fi, err := os.Stat(hook)
	if err != nil {
		return false, "no pre-push hook installed — run tools/flywheel/install-hooks.sh"
	}
	if fi.Mode()&0o111 == 0 {
		return false, "the pre-push hook is not executable, so git never runs it"
	}

	code, out, err := runPrePush(repoPath, hook, "refs/heads/main", timeout)
	switch {
	case err != nil:
		return false, "the pre-push hook could not be run: " + err.Error()
	case code == 0:
		return false, "the pre-push hook let an agent push main" + firstLine(out)
	}

	code, out, err = runPrePush(repoPath, hook, "refs/heads/bead/doctor-probe", timeout)
	switch {
	case err != nil:
		return false, "the pre-push hook could not be run: " + err.Error()
	case code != 0:
		return false, "the pre-push hook refuses a builder's own bead branch too — it is failing, not judging" + firstLine(out)
	}
	return true, "refuses main, allows a bead branch"
}

// runPrePush feeds git's own pre-push stdin format to the hook and returns its
// exit code, its output, and an error only when the hook could not be run to a
// verdict at all. A hook that answers is never an error, whichever way it went.
func runPrePush(repoPath, hook, remoteRef string, timeout time.Duration) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c := inDirContext(ctx, repoPath, hook, "origin", probeRemote)
	// FLYWHEEL_AGENT is one of the two signals the guard keys on; the other is
	// running from a linked worktree, which doctor is not. Without this the
	// guard would correctly wave the probe through as a human at a terminal,
	// and the probe would report a dead guard on a working one.
	c.Env = append(c.Env, "FLYWHEEL_AGENT="+probeAgent)
	c.Stdin = strings.NewReader(prePushLine(remoteRef))
	// Cancelling kills the hook, not the children it forked, and CombinedOutput
	// then waits on a pipe those children still hold — so `sleep 600` in a hook
	// outlived a 20s deadline by ten minutes. WaitDelay is what actually bounds
	// this; the context alone bounds nothing that spawns anything.
	c.WaitDelay = min(waitDelay, timeout)

	out, err := c.CombinedOutput()
	if ctx.Err() != nil {
		return 0, string(out), fmt.Errorf("no verdict in %s", timeout)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out), nil
	}
	return 0, string(out), err
}

// prePushLine is one line of git's pre-push stdin:
//
//	<local-ref> <local-sha> <remote-ref> <remote-sha>
//
// All four fields, with object-id-shaped shas. A line written with blanks for
// the shas collapses under `read -r`: the fields shift left, remote_ref comes
// out empty, and push-guard.sh skips the line — which would make every probe
// pass against every guard, working or not.
//
// An all-zero remote sha says the remote does not have the branch yet, so this
// is the create case. The guard refuses any write to main, create included.
func prePushLine(remoteRef string) string {
	return fmt.Sprintf("refs/heads/probe %s %s %s\n", probeLocalOID, remoteRef, zeroOID)
}

// prePushHook is the file git would run, which is not always .git/hooks: a repo
// that sets core.hooksPath moves it, and probing a file git ignores is exactly
// the "looks installed" failure this exists to catch. ADR 0012 keeps
// core.hooksPath unset here, but doctor runs against repos that have not
// adopted it yet.
func prePushHook(repoPath string) (string, error) {
	if out, err := inDir(repoPath, "git", "config", "--get", "core.hooksPath").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return underRepo(repoPath, filepath.Join(p, "pre-push")), nil
		}
	}
	out, err := inDir(repoPath, "git", "rev-parse", "--git-path", "hooks/pre-push").Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", errors.New("git named no hooks directory")
	}
	return underRepo(repoPath, p), nil
}

// underRepo resolves what git answered. `rev-parse --git-path` is relative to
// the caller's directory, which for every subprocess here is the repo root.
func underRepo(repoPath, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(repoPath, p)
}

// firstLine quotes the hook's own first line of output, so a failure names the
// thing that produced it instead of sending the reader off to run the hook by
// hand. Truncated by runes, because a byte cut can split one.
func firstLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isDecoration(line) {
			// lefthook opens with a drawn banner, so the first non-empty line
			// was "╭─────────╮" — a reason that tells the reader nothing about
			// why their guard is dead. Skip the frame, quote the sentence.
			continue
		}
		if r := []rune(line); len(r) > 120 {
			line = string(r[:120]) + "…"
		}
		return ": " + line
	}
	return ""
}

// isDecoration reports whether a line belongs to a runner's drawn frame.
//
// Any box-drawing rune anywhere marks the line, because lefthook puts its
// title INSIDE the box ("│ 🥊 lefthook │") — testing for a line that is only
// frame skips the borders and then quotes the title instead. Hooks do not draw
// boxes around their own messages, so this costs nothing real.
func isDecoration(line string) bool {
	for _, r := range line {
		if (r >= 0x2500 && r <= 0x257F) || (r >= 0x2580 && r <= 0x259F) {
			return true
		}
	}
	return false
}
