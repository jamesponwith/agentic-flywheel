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
			run := func(args ...string) error {
				// Hermetic: this package has no inDir, and a bare exec.Command
				// here would act on the repository running the test — the exact
				// bug that published 36 commits under a stranger's name.
				c := exec.Command("git", args...)
				c.Dir = dir
				c.Env = hermeticEnv()
				out, err := c.CombinedOutput()
				if err != nil && !strings.Contains(string(out), "CONFLICT") {
					return err
				}
				return err
			}
			log := filepath.Join(dir, ".flywheel", "agent-log.jsonl")
			if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(path, body string) {
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write(log, `{"ts":"2026-01-01T00:00:00Z","event":"base"}`+"\n")
			if tc.attributes {
				b, err := os.ReadFile("../../.gitattributes")
				if err != nil {
					t.Skip(".gitattributes not present")
				}
				write(filepath.Join(dir, ".gitattributes"), string(b))
			}
			for _, a := range [][]string{{"config", "user.email", "fixture@fleet.invalid"},
				{"config", "user.name", "fixture"}, {"add", "-A"}, {"commit", "-qm", "base"},
				{"checkout", "-q", "-b", "feature"}} {
				if err := run(a...); err != nil {
					t.Fatalf("git %v: %v", a, err)
				}
			}
			write(log, `{"ts":"2026-01-01T00:00:00Z","event":"base"}`+"\n"+
				`{"ts":"2026-01-02T00:00:00Z","event":"from-branch"}`+"\n")
			_ = run("commit", "-qam", "branch")
			_ = run("checkout", "-q", "main")
			write(log, `{"ts":"2026-01-01T00:00:00Z","event":"base"}`+"\n"+
				`{"ts":"2026-01-03T00:00:00Z","event":"from-main"}`+"\n")
			_ = run("commit", "-qam", "main")

			err := run("merge", "-q", "--no-edit", "feature")
			merged := err == nil
			if merged != tc.wantMerge {
				t.Fatalf("merged = %v, want %v", merged, tc.wantMerge)
			}
			if !merged {
				return // the control case; nothing more to assert
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
