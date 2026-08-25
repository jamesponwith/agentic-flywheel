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
	script, err := filepath.Abs("nightly.sh")
	if err != nil {
		t.Fatal(err)
	}
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
	// /usr/bin:/bin, not an empty PATH: the point is to reproduce the timer's
	// environment, which has coreutils and git but no go — not one so bare the
	// script dies on dirname before it can check anything.
	dir := scratch(t)
	out, code := runNightly(t, dir, "/usr/bin:/bin", "run")
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
	runNightly(t, dir, "/usr/bin:/bin", "run")
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
