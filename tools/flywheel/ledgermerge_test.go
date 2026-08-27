package flywheel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppendOnlyLedgersMergeAsUnion proves the .gitattributes rule is
// load-bearing by running the merge that used to fail.
//
// Every branch appends to the audit log, so every branch collided with every
// merge — three times in one afternoon here. Each manual resolution was a
// chance to drop a record by picking a side, on the one file whose whole
// argument is that it is append-only.
func TestAppendOnlyLedgersMergeAsUnion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		attributes bool
		wantMerge  bool
	}{
		{"with the union rule", true, true},
		{"without it, to show the rule does something", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := scratch(t)
			// Every setup step is checked. The first version ignored their
			// errors, so when one behaved differently on the runner the test
			// silently exercised nothing and failed in BOTH directions — a
			// vacuous test, in the test written to catch vacuous tests.
			git := func(args ...string) string {
				t.Helper()
				c := exec.Command("git", args...)
				c.Dir = dir
				c.Env = hermeticEnv()
				out, err := c.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
				return string(out)
			}
			log := filepath.Join(dir, ".flywheel", "agent-log.jsonl")
			if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(body string) {
				t.Helper()
				if err := os.WriteFile(log, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			const base = `{"ts":"2026-01-01T00:00:00Z","event":"base"}` + "\n"

			write(base)
			if tc.attributes {
				b, err := os.ReadFile("../../.gitattributes")
				if err != nil {
					t.Skip(".gitattributes not present")
				}
				if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			git("config", "user.email", "fixture@invalid")
			git("config", "user.name", "fixture")
			git("add", "-A")
			git("commit", "-qm", "base")
			root := strings.TrimSpace(git("rev-parse", "HEAD"))
			// Capture the default branch rather than assuming "main". scratch()
			// runs `git init` with no -b, so the name comes from the host's
			// init.defaultBranch — "main" here, something else on the runner,
			// where `checkout main` failed with exit 1. Same class as the two
			// PATH assumptions today: a fixture that only holds where it was
			// written.
			trunk := strings.TrimSpace(git("rev-parse", "--abbrev-ref", "HEAD"))

			git("checkout", "-q", "-b", "feature")
			write(base + `{"ts":"2026-01-02T00:00:00Z","event":"from-branch"}` + "\n")
			git("commit", "-qam", "branch")
			feature := strings.TrimSpace(git("rev-parse", "HEAD"))

			git("checkout", "-q", trunk)
			write(base + `{"ts":"2026-01-03T00:00:00Z","event":"from-main"}` + "\n")
			git("commit", "-qam", "main")
			mainTip := strings.TrimSpace(git("rev-parse", "HEAD"))

			// Assert the fixture actually diverged. Without this, a merge that
			// fast-forwards proves nothing and reads as a pass.
			if feature == mainTip || feature == root || mainTip == root {
				t.Fatalf("fixture did not diverge: root=%s feature=%s main=%s", root, feature, mainTip)
			}

			c := exec.Command("git", "merge", "-q", "--no-edit", "feature")
			c.Dir = dir
			c.Env = hermeticEnv()
			out, err := c.CombinedOutput()
			merged := err == nil
			if merged != tc.wantMerge {
				t.Fatalf("merged = %v, want %v\n%s", merged, tc.wantMerge, out)
			}
			if !merged {
				if !strings.Contains(string(out), "CONFLICT") {
					t.Errorf("failed for some reason other than a conflict:\n%s", out)
				}
				return
			}
			body, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"base", "from-branch", "from-main"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("union dropped the %q record:\n%s", want, body)
				}
			}
		})
	}
}
