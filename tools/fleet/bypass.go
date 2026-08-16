// Bypass detection. SPEC.md's best line is "if a gate gets bypassed twice,
// delete it or automate it" — and until something counts bypasses, that is a
// slogan rather than a rule.
//
// Three signals, all recoverable after the fact from git alone. None of them
// requires the bypasser to have been honest about it, which is the point:
//
//	direct-to-main   a commit on main that belongs to no merged PR, i.e. the
//	                 Validate gate never ran on it
//	no-verify        a commit whose tree the pre-commit hook would have
//	                 rejected (unformatted Go), so the hook was skipped
//	stale-quarantine a test quarantined longer than its deadline
//
// A gate over its bypass budget gets a bead proposing its own deletion. That
// is the rule enforcing itself.
package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type Bypass struct {
	Repo   string `json:"repo"`
	Kind   string `json:"kind"`
	Commit string `json:"commit"`
	Detail string `json:"detail"`
}

// squashMerged matches GitHub's squash-merge subject convention, "… (#123)".
// Squash merges leave no merge commit, so without this every squash-merged PR
// looks like a direct push. The first live run reported exactly that: thirteen
// false positives across two repos, including commits that plainly went through
// a PR. A detector that noisy gets ignored within a day, which is the failure
// mode this whole bead exists to prevent.
var squashMerged = regexp.MustCompile(`\(#\d+\)$`)

// bookkeeping subjects are not code and were never meant to pass a code gate.
// The pattern deliberately mirrors .goreleaser.yaml's changelog exclusions —
// the project already decided these are not changes, so the bypass detector
// should not disagree with the changelog.
var bookkeeping = regexp.MustCompile(`^(bd|beads|chore|docs)(\(|:)`)

// botAuthors commit automation output. A workflow committing a DORA snapshot
// back to the repo is the Learn stage working, not someone skipping a gate.
var botAuthors = map[string]bool{
	"github-actions":      true,
	"github-actions[bot]": true,
	"dependabot[bot]":     true,
	"web-flow":            true,
}

// mergedPRCommits returns the set of commits reachable as merge-commit parents
// on main — everything that arrived through a merge-commit PR.
func mergedPRCommits(dir string) (map[string]bool, error) {
	// Merge commits on main are PR merges; their second parent's history is
	// the PR's commits.
	out, err := git(dir, "rev-list", "--merges", "main")
	if err != nil {
		return nil, err
	}
	in := map[string]bool{}
	for _, m := range strings.Fields(out) {
		side, err := git(dir, "rev-list", m+"^2", "--not", m+"^1")
		if err != nil {
			continue
		}
		for _, c := range strings.Fields(side) {
			in[c] = true
		}
		in[m] = true
	}
	return in, nil
}

// DetectBypasses walks main's history since `since` (a git revision or date).
func DetectBypasses(repo, dir, since string) ([]Bypass, error) {
	var out []Bypass

	viaPR, err := mergedPRCommits(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repo, err)
	}

	args := []string{"rev-list", "--no-merges", "main"}
	if since != "" {
		args = append(args, "--since="+since)
	}
	list, err := git(dir, args...)
	if err != nil {
		return nil, err
	}

	for _, c := range strings.Fields(list) {
		if viaPR[c] {
			continue
		}
		info, _ := git(dir, "log", "-1", "--format=%s%x00%an", c)
		parts := strings.SplitN(strings.TrimSpace(info), "\x00", 2)
		subject := strings.TrimSpace(parts[0])
		author := ""
		if len(parts) > 1 {
			author = strings.TrimSpace(parts[1])
		}
		switch {
		case squashMerged.MatchString(subject):
			continue // squash-merged PR: it went through the gate
		case botAuthors[author]:
			continue // automation output, not a skipped gate
		case bookkeeping.MatchString(subject):
			continue // not code; the changelog excludes these too
		}
		out = append(out, Bypass{
			Repo: repo, Kind: "direct-to-main", Commit: c[:min(8, len(c))],
			Detail: subject + " — never passed the PR gate",
		})
	}
	return out, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	return string(b), err
}

// BypassBudget is how many bypasses of one kind are tolerated before the rule
// fires. Two, per SPEC.md — the rule names the number itself.
const BypassBudget = 2

// OverBudget groups bypasses by kind and reports those past the budget. These
// are the gates that should be deleted or automated, named by their own data.
func OverBudget(bs []Bypass) map[string]int {
	counts := map[string]int{}
	for _, b := range bs {
		counts[b.Repo+"/"+b.Kind]++
	}
	over := map[string]int{}
	for k, n := range counts {
		if n > BypassBudget {
			over[k] = n
		}
	}
	return over
}
