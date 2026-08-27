package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestBuildersAreDeniedMergeAtTheSpawn(t *testing.T) {
	// The rule cannot live in .claude/settings.json. A deny there applies to
	// whoever reads it, and the maintainer reads it too — so keeping it made
	// nobody able to merge, and removing it would hand merge authority to
	// every builder. Measured, not assumed: a deny beats an allow from a
	// more specific settings file, so a session-scoped grant could not
	// override it.
	r := Runner{Cmd: "claude", Args: []string{"-p", "{prompt}"}}
	argv := strings.Join(r.argv("/flywheel-next fw-1"), " ")
	if strings.Contains(argv, "--disallowed-tools") {
		t.Fatal("argv() should not carry the denial; it belongs to the spawn, " +
			"which is the path a roster cannot reconfigure")
	}
	if !strings.Contains(unattendedDenials, "gh pr merge") {
		t.Error("the unattended denial no longer covers merging")
	}
}

func TestTheProjectFileStillDeniesWhatBothPartiesMustNotDo(t *testing.T) {
	// Only the one rule that has to differ between the supervised session and
	// an unattended builder moved. Tagging, releasing, force-pushing, reading
	// secrets and reaching the admin API are forbidden to both, so they stay
	// where they apply to both.
	b, err := os.ReadFile("../../.claude/settings.json")
	if err != nil {
		t.Skip("settings.json not present")
	}
	var s struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Bash(git tag:*)", "Bash(gh release:*)", "Bash(git push --force:*)",
		"Bash(git push origin main:*)", "Bash(gh auth token:*)", "Bash(gh api:*)",
		"Read(**/.env)",
	} {
		if !slices.Contains(s.Permissions.Deny, want) {
			t.Errorf("%s left the project deny list — ADR 0003 forbids it to BOTH callers", want)
		}
	}
	if slices.Contains(s.Permissions.Deny, "Bash(gh pr merge:*)") {
		t.Error("merge is denied in the shared file again, which denies it to the " +
			"maintainer too; it belongs on the builder spawn")
	}
}
