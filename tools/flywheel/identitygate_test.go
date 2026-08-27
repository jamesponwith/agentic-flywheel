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
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// unownable reports whether a domain is one nobody can ever register. RFC 2606
// and RFC 6761 set these aside for exactly this purpose: no registry will sell
// one, and no mail to one can be delivered or verified.
//
// Note what this does and does not buy. GitHub attributes a commit by matching
// the author string against the addresses on an account, and "d@d" was already
// unregistrable when it matched github.com/dimas1 — so unregistrability alone
// is not what protects us. What it buys is that nobody can *acquire* one of
// these addresses in future to capture attribution, and that a leak cannot be
// turned into deliverable mail. Whether some account already carries the literal
// "d@invalid" is unknowable from here (fw-0rf).
func unownable(domain string) bool {
	for _, reserved := range []string{"invalid", "test", "example", "localhost",
		"example.com", "example.org", "example.net"} {
		if domain == reserved || strings.HasSuffix(domain, "."+reserved) {
			return true
		}
	}
	return false
}

// gateRefuses mirrors the pre-commit identity rule in lefthook.yml: a real
// domain has a dot, so one without is a fixture that leaked into .git/config.
func gateRefuses(domain string) bool { return !strings.Contains(domain, ".") }

func domainOf(email string) string {
	_, domain, _ := strings.Cut(email, "@")
	return domain
}

// emailLiteral matches an email-shaped Go string literal.
//
// Deliberately not `"user.email", "<value>"`. That narrower pattern reads as
// the safer choice and is the weaker one: bypass_test.go hands git an identity
// as `--author "name <bot@gh>"`, which never spells "user.email" at all, and
// .gh is Ghana's ccTLD — a registrable domain the bead exists to remove, sitting
// in the tree, invisible to the pattern that was supposed to find it. Matching
// every address-shaped literal covers both spellings and any third one.
var emailLiteral = regexp.MustCompile(`"([A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+)"`)

// gateOwnFile is exempt from the scan, by path from the repo root rather than
// by base name. It is the only file that must hold real addresses — they are
// the negative vectors proving the gate does not reject actual humans — but
// exempting the *name* would let a second `identitygate_test.go` in any other
// package be born exempt, in the package where every leak so far has lived.
const gateOwnFile = "tools/flywheel/identitygate_test.go"

type fixtureIdentity struct {
	email, where string
	line         int
}

// scanFixtureEmails greps the repo for the identities its helpers hand to git.
// The list is derived rather than hand-maintained, because a hand-maintained
// list is one that the next test file is not on.
//
// Every .go file, not just _test.go: the moment a helper like gitRepo() gets
// shared between packages it stops being a _test.go and would leave coverage
// silently. No non-test file contains an address today, so the wider net is free.
//
// ponytail: line comments are skipped, so this cannot read prose. The ceiling
// is that a block comment or an address built by concatenation ("bot@"+tld)
// passes unseen. Upgrade path is a go/ast walk over string literals if either
// shape ever appears.
func scanFixtureEmails(t *testing.T) []fixtureIdentity {
	t.Helper()
	root := repoRoot(t)
	var found []fixtureIdentity
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if filepath.ToSlash(rel) == gateOwnFile {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range emailLiteral.FindAllStringSubmatch(line, -1) {
				found = append(found, fixtureIdentity{
					email: m[1], where: filepath.ToSlash(rel), line: i + 1})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo: %v", err)
	}
	// A scan that matches nothing passes forever and protects nothing. If the
	// helpers stop using this shape, this test must fail loudly rather than
	// quietly approve an empty set.
	if len(found) == 0 {
		t.Fatal("no fixture addresses found at all — the scan is broken, not the repo clean")
	}
	return found
}

// repoRoot walks up from the test's working directory to the module root, so
// the scan cannot silently narrow to a subtree if this package is ever moved.
// A hardcoded "../.." would still find most fixtures after such a move, which
// is exactly the kind of partial coverage that reports success.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory — cannot locate the repo root")
		}
		dir = parent
	}
}

// The acceptance criterion for fw-0rf, asserted against every fixture in the
// repo rather than a hand-picked example.
//
// Both properties are required, and the second is the one that is easy to lose.
// Moving fixtures onto reserved domains makes a leak anonymous, but "foo.invalid"
// has a dot, so it would walk straight through the pre-commit gate that exists
// to catch leaks in the first place — the gate would stop working silently, at
// exactly the moment the fixtures got safer. A bare reserved TLD ("d@invalid")
// satisfies both, so the two controls compose instead of trading off.
func TestEveryFixtureIdentityIsUnownableAndCaughtByTheGate(t *testing.T) {
	for _, f := range scanFixtureEmails(t) {
		domain := domainOf(f.email)
		if !unownable(domain) {
			t.Errorf("%s:%d: fixture %q uses a registrable domain — a leak would attribute\n"+
				"  to whoever owns it. Use a reserved TLD: d@invalid", f.where, f.line, f.email)
			continue
		}
		if !gateRefuses(domain) {
			t.Errorf("%s:%d: fixture %q is unownable but invisible to the pre-commit identity\n"+
				"  gate, which refuses only domains without a dot. Use the bare TLD: d@invalid",
				f.where, f.line, f.email)
		}
	}
}

func TestTheGateRejectsFixturesAndAcceptsRealAddresses(t *testing.T) {
	for _, email := range []string{"d@d", "t@t", "conformance@test", "d@invalid", "r@invalid"} {
		if !gateRefuses(domainOf(email)) {
			t.Errorf("fixture %q would pass the gate; the dot rule does not cover it", email)
		}
	}
	for _, email := range []string{
		"jponwith@sandiego.edu",
		"noreply@anthropic.com",
		"github-actions@github.com",
		"a@b.co",
	} {
		if gateRefuses(domainOf(email)) {
			t.Errorf("real address %q would be rejected by the gate", email)
		}
	}
}

func TestUnownableAcceptsOnlyReservedDomains(t *testing.T) {
	for _, domain := range []string{"invalid", "test", "example", "localhost",
		"fleet.invalid", "example.com", "example.org", "example.net", "a.example.com"} {
		if !unownable(domain) {
			t.Errorf("%q is reserved by RFC 2606/6761 but was called ownable", domain)
		}
	}
	// The registrable ones, including the near-misses: a domain that merely
	// contains a reserved word, or ends in one without the dot boundary, is an
	// ordinary domain somebody can buy.
	for _, domain := range []string{"d", "gmail.com", "github.com", "sandiego.edu",
		"invalid.com", "example.io", "notinvalid", "testing", "myexample.com"} {
		if unownable(domain) {
			t.Errorf("%q is registrable but was called unownable", domain)
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
			// Matched by path, not base name — skipping the name alone would
			// also exempt a future ../fleet/identitygate_test.go, in the package
			// where every leak so far has actually lived.
			if !strings.HasSuffix(name, "_test.go") || dir+"/"+name == "../flywheel/identitygate_test.go" {
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
