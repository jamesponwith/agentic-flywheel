package flywheel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// guardIn runs guard.sh inside dir with its state directory pointed somewhere
// the test owns, so the real ledger is never touched.
func guardIn(t *testing.T, dir, state, agent string, args ...string) (string, int) {
	t.Helper()
	c := exec.Command(guardPath(t), args...)
	c.Dir = dir
	c.Env = append(hermeticEnv(), "XDG_STATE_HOME="+state)
	if agent != "" {
		c.Env = append(c.Env, "FLYWHEEL_AGENT="+agent)
	}
	out, err := c.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("guard.sh %v: %v\n%s", args, err, out)
	}
	return string(out), code
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestTheLedgerSurvivesAGitRevert(t *testing.T) {
	// .flywheel/agent-log.jsonl is a tracked file, so `git checkout --`,
	// `git reset --hard` and `git stash` revert it like any other — and every
	// one of those is an ordinary thing to run while tidying up. It happened
	// twice in one session and took the only genuine cost records both times.
	// A record a routine command can erase is not evidence.
	dir := scratch(t)
	state := t.TempDir()
	repo := filepath.Base(dir)

	guardIn(t, dir, state, "fleet/builder", "log", "bead.cost", "usd=2.3992")
	guardIn(t, dir, state, "fleet/builder", "log", "bead.pr_opened")

	mirror := filepath.Join(state, "flywheel", "ledger", repo+"-agent-log.jsonl")
	if got := len(lines(t, mirror)); got != 2 {
		t.Fatalf("mirror has %d records, want 2 — nothing durable was written", got)
	}

	// The revert.
	log := filepath.Join(dir, ".flywheel", "agent-log.jsonl")
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := guardIn(t, dir, state, "", "restore-log")
	if code != 0 {
		t.Fatalf("restore-log failed: %s", out)
	}
	got := lines(t, log)
	if len(got) != 2 {
		t.Fatalf("recovered %d records, want 2:\n%s", len(got), out)
	}
	if !strings.Contains(strings.Join(got, "\n"), "2.3992") {
		t.Error("the cost record did not come back; the mirror is not authoritative")
	}
}

func TestRestoreIsAUnionNotAnOverwrite(t *testing.T) {
	// The in-repo copy holds records older than the mirror itself, so an
	// overwrite would trade one kind of loss for another.
	dir := scratch(t)
	state := t.TempDir()
	log := filepath.Join(dir, ".flywheel", "agent-log.jsonl")

	guardIn(t, dir, state, "fleet/builder", "log", "mirrored.event")
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ts":"2000-01-01T00:00:00Z","event":"older.than.the.mirror"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if out, code := guardIn(t, dir, state, "", "restore-log"); code != 0 {
		t.Fatalf("restore-log failed: %s", out)
	}
	joined := strings.Join(lines(t, log), "\n")
	for _, want := range []string{"mirrored.event", "older.than.the.mirror"} {
		if !strings.Contains(joined, want) {
			t.Errorf("restore dropped %q:\n%s", want, joined)
		}
	}
}

func TestRestoreDoesNotDuplicate(t *testing.T) {
	// Run it twice; a union that duplicates would inflate every count derived
	// from the ledger, including spend.
	dir := scratch(t)
	state := t.TempDir()
	guardIn(t, dir, state, "fleet/builder", "log", "bead.cost", "usd=1.00")
	log := filepath.Join(dir, ".flywheel", "agent-log.jsonl")
	guardIn(t, dir, state, "", "restore-log")
	guardIn(t, dir, state, "", "restore-log")
	if got := len(lines(t, log)); got != 1 {
		t.Errorf("%d records after two restores, want 1 — spend would double", got)
	}
}

func TestAMissingMirrorIsAnError(t *testing.T) {
	// Silently succeeding here would report a recovery that did not happen.
	dir := scratch(t)
	out, code := guardIn(t, dir, t.TempDir(), "", "restore-log")
	if code == 0 {
		t.Errorf("claimed to restore from a mirror that does not exist: %s", out)
	}
}
