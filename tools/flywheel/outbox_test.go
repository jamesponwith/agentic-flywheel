// Tests for outbox.sh, the handoff for files a builder may not write
// (ADR 0015). Same shape as guard_test.go: a Go test around a shell script so
// the refusals run under `go test -short ./...` on every commit.
//
// Entry paths are relative to the outbox root and map to .claude/<path>; the
// prefix is implied because the harness refuses any path containing
// `.claude/`, an outbox mirror of it included.
package flywheel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func outboxPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("outbox.sh")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// runOutbox invokes outbox.sh inside dir. agent="" means a human checkout;
// anything else is exported as FLYWHEEL_AGENT, the builder's mark. Invoked via
// bash because the working-tree file may lack the executable bit the index
// carries (the builder that wrote it could not chmod).
func runOutbox(t *testing.T, dir, agent string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{outboxPath(t)}, args...)...)
	cmd.Dir = dir
	env := make([]string, 0, len(hermeticEnv())+3)
	for _, kv := range hermeticEnv() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "FLYWHEEL_AGENT" || k == "XDG_STATE_HOME" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"FLYWHEEL_HOME="+filepath.Join(dir, "home"),
		"XDG_STATE_HOME="+filepath.Join(dir, "state"))
	if agent != "" {
		env = append(env, "FLYWHEEL_AGENT="+agent)
	}
	cmd.Env = env
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running outbox.sh: %v", err)
	}
	return out.String(), errb.String(), code
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitC(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = hermeticEnv()
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	gitC(t, dir, "add", "-A")
	gitC(t, dir, "-c", "user.email=test@invalid", "-c", "user.name=test",
		"commit", "-q", "-m", "setup")
}

func TestOutboxApplyMovesAndStages(t *testing.T) {
	dir := scratch(t)
	write(t, filepath.Join(dir, ".claude/skills/foo/SKILL.md"), "hello v1\n")
	write(t, filepath.Join(dir, ".flywheel/outbox/skills/foo/SKILL.md"), "hello v2\n")
	write(t, filepath.Join(dir, ".flywheel/outbox/new.md"), "brand new\n")
	commitAll(t, dir)

	stdout, stderr, code := runOutbox(t, dir, "", "apply")
	if code != 0 {
		t.Fatalf("apply: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "staged 2 file") {
		t.Errorf("apply stdout %q: want 'staged 2 file'", stdout)
	}
	for path, want := range map[string]string{
		".claude/skills/foo/SKILL.md": "hello v2\n",
		".claude/new.md":              "brand new\n",
	} {
		b, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", path, b, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".flywheel/outbox")); !os.IsNotExist(err) {
		t.Errorf("outbox dir still present after apply (err=%v)", err)
	}
	// Both sides staged: the targets and the removal of their outbox copies.
	stcmd := exec.Command("git", "-C", dir, "diff", "--cached", "--name-status", "--no-renames")
	stcmd.Env = hermeticEnv()
	st, err := stcmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"M\t.claude/skills/foo/SKILL.md",
		"A\t.claude/new.md",
		"D\t.flywheel/outbox/skills/foo/SKILL.md",
		"D\t.flywheel/outbox/new.md",
	} {
		if !strings.Contains(string(st), want) {
			t.Errorf("staged diff missing %q in:\n%s", want, st)
		}
	}
	var logged bool
	for _, line := range readLines(t, filepath.Join(dir, ".flywheel/agent-log.jsonl")) {
		if strings.Contains(line, `"event":"outbox.applied"`) && strings.Contains(line, `"files":"2"`) {
			logged = true
		}
	}
	if !logged {
		t.Error("no outbox.applied files=2 record in the audit log")
	}
}

// Every refusal must leave the tree untouched: no target written, no partial
// apply. A handoff that half-happens is worse than one that fails loudly.
func TestOutboxRefusals(t *testing.T) {
	goodEntry := ".flywheel/outbox/good.md"
	// escapeSetup points .claude/skills at a directory outside the repo, so
	// the entry skills/x.md resolves outside .claude/ — the one way the
	// outbox mapping can name foreign ground.
	escapeSetup := func(t *testing.T, dir string) {
		if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(dir, ".claude/skills")); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, ".flywheel/outbox/skills/x.md"), "escape\n")
	}
	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string)
		agent    string
		cmd      string
		wantCode int
		wantErr  string
	}{
		{
			name:     "apply refuses a builder identity from the environment",
			agent:    "agentic-flywheel/builder",
			cmd:      "apply",
			wantCode: 1,
			wantErr:  "human's step",
		},
		{
			name: "apply refuses inside a spawner worktree",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, ".flywheel/agent"), "fleet/builder-go\n")
			},
			cmd:      "apply",
			wantCode: 1,
			wantErr:  "human's step",
		},
		{
			name:     "apply refuses a target escaping .claude, applying nothing",
			setup:    escapeSetup,
			cmd:      "apply",
			wantCode: 2,
			wantErr:  "resolves outside",
		},
		{
			name:     "status refuses a target escaping .claude",
			setup:    escapeSetup,
			cmd:      "status",
			wantCode: 2,
			wantErr:  "resolves outside",
		},
		{
			name: "apply refuses a symlink entry",
			setup: func(t *testing.T, dir string) {
				if err := os.MkdirAll(filepath.Join(dir, ".flywheel/outbox"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/etc/hostname",
					filepath.Join(dir, ".flywheel/outbox/link.md")); err != nil {
					t.Fatal(err)
				}
			},
			cmd:      "apply",
			wantCode: 2,
			wantErr:  "non-regular",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := scratch(t)
			// The good entry rides along in every case: a refusal anywhere
			// must stop it from being applied too.
			write(t, filepath.Join(dir, goodEntry), "fine content\n")
			if tt.setup != nil {
				tt.setup(t, dir)
			}
			commitAll(t, dir)

			_, stderr, code := runOutbox(t, dir, tt.agent, tt.cmd)
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d (stderr %q)", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr %q: want it to contain %q", stderr, tt.wantErr)
			}
			if _, err := os.Stat(filepath.Join(dir, ".claude/good.md")); !os.IsNotExist(err) {
				t.Errorf(".claude/good.md was applied despite the refusal (err=%v)", err)
			}
			if _, err := os.Stat(filepath.Join(dir, goodEntry)); err != nil {
				t.Errorf("outbox entry disturbed by refused %s: %v", tt.cmd, err)
			}
		})
	}
}

func TestOutboxEmpty(t *testing.T) {
	for _, cmd := range []string{"status", "diff", "apply"} {
		t.Run(cmd, func(t *testing.T) {
			dir := scratch(t)
			stdout, stderr, code := runOutbox(t, dir, "", cmd)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			if !strings.Contains(stdout, "outbox empty") {
				t.Errorf("stdout %q: want 'outbox empty'", stdout)
			}
		})
	}
}

func TestOutboxStatusAndDiff(t *testing.T) {
	dir := scratch(t)
	write(t, filepath.Join(dir, ".claude/skills/foo/SKILL.md"), "hello v1\n")
	write(t, filepath.Join(dir, ".flywheel/outbox/skills/foo/SKILL.md"), "hello v2\n")
	write(t, filepath.Join(dir, ".flywheel/outbox/new.md"), "brand new\n")
	commitAll(t, dir)

	stdout, stderr, code := runOutbox(t, dir, "", "status")
	if code != 0 {
		t.Fatalf("status: exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"modifies  .claude/skills/foo/SKILL.md", "new       .claude/new.md"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status stdout %q: want %q", stdout, want)
		}
	}

	stdout, stderr, code = runOutbox(t, dir, "", "diff")
	if code != 0 {
		t.Fatalf("diff: exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"-hello v1", "+hello v2", "+brand new"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("diff stdout %q: want %q", stdout, want)
		}
	}
}
