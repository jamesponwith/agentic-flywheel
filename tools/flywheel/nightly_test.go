// The nightly runner has to be honest about why it stopped.
//
// The first genuinely unattended night died in seconds on "failed to run
// command 'go': No such file or directory" and wrote a digest saying the run
// had exceeded 90 minutes. Both halves were wrong and each has its own test:
// the timer could not find go, and every non-zero exit was reported as a
// timeout.
package flywheel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runNightly runs nightly.sh in dir with PATH replaced, so the timer's minimal
// environment can be reproduced on purpose.
func runNightly(t *testing.T, dir, path string, args ...string) (string, int) {
	t.Helper()
	// nightly.sh cds to the repository containing itself, so running the real
	// one operates on THIS repo whatever c.Dir says — it read the maintainer's
	// own kill switch. Copy it, and guard.sh beside it, into the scratch repo
	// so the test acts only on what it created.
	tools := filepath.Join(dir, "tools", "flywheel")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"nightly.sh", "guard.sh"} {
		dst := filepath.Join(tools, f)
		// Only if absent: a test that substitutes its own guard.sh must not
		// have it silently replaced by the real one on the next invocation,
		// which is how three subtests all exercised the same path.
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(tools, "nightly.sh")
	c := exec.Command("bash", script)
	c.Args = append(c.Args, args...)
	c.Dir = dir
	c.Env = append(hermeticEnv(), "PATH="+path, "HOME="+dir)
	out, err := c.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("nightly.sh %v: %v\n%s", args, err, out)
	}
	return string(out), code
}

func TestNightlyNamesTheMissingCommand(t *testing.T) {
	// A systemd user service gets a minimal PATH — no go, no bd, no gh. The
	// run must say which command is missing and where it looked, not spend the
	// ceiling discovering it inside a subprocess.
	// The PATH is BUILT, not borrowed. Assuming "/usr/bin:/bin has coreutils
	// but no go" is true on the machine this was written on and false on the
	// CI runner, where go sits in /usr/bin — so the preflight passed, the run
	// proceeded, and the test spent 13 seconds downloading a toolchain into
	// its own temp dir before failing. That is the same shape as fw-c1l: a
	// check that holds where it was written and nowhere else.
	dir := scratch(t)
	out, code := runNightly(t, dir, pathWithout(t, "go"), "run")
	if code == 0 {
		t.Fatalf("reported success with no toolchain at all:\n%s", out)
	}
	if !strings.Contains(out, "go") || !strings.Contains(out, "PATH") {
		t.Errorf("does not name the missing command or the PATH searched:\n%s", out)
	}
	if strings.Contains(out, "exceeded") {
		t.Errorf("called a missing command a timeout:\n%s", out)
	}
}

func TestNightlyWritesNoDigestItCannotStand(t *testing.T) {
	// A digest is the record of a night. One describing a run that never
	// started is worse than none, because it reads as evidence later.
	dir := scratch(t)
	runNightly(t, dir, pathWithout(t, "go"), "run")
	entries, err := os.ReadDir(filepath.Join(dir, ".flywheel", "digests"))
	if err == nil && len(entries) > 0 {
		t.Errorf("wrote %d digest(s) for a run that never started", len(entries))
	}
}

func TestInstallCapturesAWorkingPath(t *testing.T) {
	// The unit must carry a PATH that contains go, or the timer reproduces the
	// exact failure this fixes. Checked by reading the generated unit rather
	// than by installing, so the test never touches the user's real timers.
	b, err := os.ReadFile("nightly.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "Environment=PATH=$PATH") {
		t.Error("the generated unit does not carry a PATH; a systemd user service " +
			"gets a minimal one and the run dies before it starts")
	}
}

func TestOnlyOneTwoFourIsATimeout(t *testing.T) {
	// The regression itself: `|| echo "(run exceeded ...)"` fired on every
	// non-zero exit. Assert the script distinguishes 124 from the rest.
	b, err := os.ReadFile("nightly.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "124)") {
		t.Error("nothing special-cases exit 124, so a timeout cannot be told from a crash")
	}
	if strings.Contains(src, `-execute 2>&1 || \`) {
		t.Error("still collapses every failure into one message")
	}
}

// pathWithout builds a PATH containing the commands nightly.sh needs before its
// preflight, and deliberately not the named one.
//
// Symlinks into a directory the test owns, so the result does not depend on
// what happens to live in /usr/bin on the machine running it. Everything the
// script touches before `command -v go` must be here, or it dies on the wrong
// thing and the test proves nothing.
func pathWithout(t *testing.T, excluded string) string {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"git", "dirname", "basename", "date", "mkdir",
		"cat", "sed", "tee", "head", "tail", "grep", "wc", "ls", "timeout", "env",
		"bash", "sh", "rm", "cp", "tr", "sort", "uniq", "printf", "systemd-run"} {
		if name == excluded {
			continue
		}
		src, err := exec.LookPath(name)
		if err != nil {
			continue // not every box has every one; the script tolerates that
		}
		_ = os.Symlink(src, filepath.Join(bin, name))
	}
	if _, err := exec.LookPath(excluded); err == nil {
		// Prove the exclusion is doing work: if the command exists on this
		// machine, it must NOT be reachable through the PATH we just built.
		if _, err := os.Stat(filepath.Join(bin, excluded)); err == nil {
			t.Fatalf("%q leaked into the constructed PATH; the test would pass vacuously", excluded)
		}
	}
	return bin
}

func TestAGuardThatCannotAnswerIsNotAKillSwitch(t *testing.T) {
	// nightly.sh reported "halted by the kill switch" and exited 0 whenever
	// guard.sh returned ANY non-zero code — including the codes that mean it
	// could not run at all. A broken environment then looked identical to a
	// deliberate halt, and both looked like success.
	// One real run first, purely to populate the scratch repo with the scripts.
	dir := scratch(t)
	runNightly(t, dir, pathWithout(t, "go"), "run")
	// Then swap guard.sh for ones that answer in each of the three ways.
	tools := filepath.Join(dir, "tools", "flywheel")
	cases := []struct {
		name, body string
		wantZero   bool
		wantText   string
	}{
		{"deliberately stopped", "#!/bin/sh\nexit 1\n", true, "halted by the kill switch"},
		{"cannot answer", "#!/bin/sh\nexit 127\n", false, "could not answer"},
		{"crashed", "#!/bin/sh\nexit 2\n", false, "could not answer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(tools, "guard.sh"), []byte(tc.body), 0o755); err != nil {
				t.Fatal(err)
			}
			out, code := runNightly(t, dir, pathWithout(t, "go"), "run")
			if tc.wantZero && code != 0 {
				t.Errorf("a real kill switch should exit 0, got %d\n%s", code, out)
			}
			if !tc.wantZero && code == 0 {
				t.Errorf("a guard that could not answer exited 0 — an outage reported as a clean night\n%s", out)
			}
			if !strings.Contains(out, tc.wantText) {
				t.Errorf("want %q in output, got:\n%s", tc.wantText, out)
			}
		})
	}
}
