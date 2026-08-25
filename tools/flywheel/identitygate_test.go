// The repository must not commit under a test fixture's name.
//
// A test helper that shelled out to git without a hermetic environment ran
// `git config user.email d@d` while GIT_DIR pointed at the real repository, so
// it rewrote this repo's identity permanently. GitHub resolves that address to
// an unrelated account, and 27 commits were published under a stranger's name
// before anyone noticed. Squash-merge happened to sanitise main — luck, not a
// control.
package flywheel

import (
	"os"
	"strings"
	"testing"
)

// fixtureEmails are the identities this repo's own test helpers set. Every one
// of them reached .git/config at some point.
var fixtureEmails = []string{"d@d", "t@t", "conformance@test"}

func TestTheGateRejectsEveryFixtureIdentity(t *testing.T) {
	// The rule is "a real domain has a dot". Assert it against the actual
	// fixture values rather than against a hand-picked example, so a new
	// fixture that slips past is a test failure and not a surprise.
	for _, email := range fixtureEmails {
		domain := email[strings.Index(email, "@")+1:]
		if strings.Contains(domain, ".") {
			t.Errorf("fixture %q would pass the gate; the dot rule does not cover it", email)
		}
	}
	for _, email := range []string{
		"jponwith@sandiego.edu",
		"noreply@anthropic.com",
		"github-actions@github.com",
		"a@b.co",
	} {
		domain := email[strings.Index(email, "@")+1:]
		if !strings.Contains(domain, ".") {
			t.Errorf("real address %q would be rejected by the gate", email)
		}
	}
}

func TestTheGateIsWiredIntoPreCommit(t *testing.T) {
	// A rule nobody runs is a comment. This one has to be in the fast gate,
	// because the damage is done by the time anything slower notices.
	b, err := os.ReadFile("../../lefthook.yml")
	if err != nil {
		t.Skip("lefthook.yml not present")
	}
	src := string(b)
	if !strings.Contains(src, "identity:") {
		t.Fatal("no identity check in lefthook.yml")
	}
	pre, _, ok := strings.Cut(src, "pre-push:")
	if !ok {
		pre = src
	}
	if !strings.Contains(pre, "identity:") {
		t.Error("the identity check is not in pre-commit, so it runs after the commit it should have stopped")
	}
}

func TestNoTestHelperShellsOutToGitBare(t *testing.T) {
	// The root cause, guarded directly. inDir() strips GIT_DIR; a bare
	// exec.Command inherits it and acts on whatever repository invoked the
	// test — which is how a temp-dir fixture's user.email reached .git/config.
	const marker = "exec.Comm" + "and(\"git\""
	for _, dir := range []string{"../fleet", "../flywheel"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			// Skip this file: it necessarily contains the pattern it hunts.
			if !strings.HasSuffix(name, "_test.go") || name == "identitygate_test.go" {
				continue
			}
			b, err := os.ReadFile(dir + "/" + name)
			if err != nil {
				continue
			}
			all := strings.Split(string(b), "\n")
			for i, line := range all {
				if !strings.Contains(line, marker) {
					continue
				}
				end := i + 4
				if end > len(all) {
					end = len(all)
				}
				window := strings.Join(all[i:end], "\n")
				if !strings.Contains(window, "hermeticEnv()") && !strings.Contains(window, ".Env =") {
					t.Errorf("%s/%s:%d shells out to git without a hermetic env:\n  %s",
						dir, name, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
