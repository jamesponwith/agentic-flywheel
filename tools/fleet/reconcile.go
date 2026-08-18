// Reconciliation (fw-lb8.9 / D7).
//
// Teardown is defer-only. Any abnormal exit — the nightly wall-clock timeout,
// Ctrl-C, OOM, a kill -9 — skips every defer and leaves behind a worktree, a
// branch, a written .flywheel/agent, and a held merge slot with no TTL.
//
// Nothing used to detect that. And because `git worktree add -b bead/<id>` is
// fatal when the branch already exists, the bead became permanently
// unreachable to the coordinator: one killed night removed N beads from the
// fleet's reach until a human noticed. Three were in that state when this was
// written.
//
// So every run reconciles first. The rule throughout: a leftover with commits
// is somebody's work and is never destroyed — it is reported. A leftover with
// nothing in it is swept.
package main

import (
	"fmt"
	"strings"
)

type Leftover struct {
	Repo     string `json:"repo"`
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	Commits  int    `json:"commits"`
	Action   string `json:"action"` // swept | kept | failed
	Detail   string `json:"detail"`
}

// Reconcile sweeps abandoned builder worktrees in one repo.
func Reconcile(repo Repo) ([]Leftover, error) {
	out, err := inDir(repo.Path, "git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", repo.Name, err)
	}

	var found []Leftover
	var wt, branch string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			wt = strings.TrimPrefix(line, "worktree ")
			branch = ""
		case strings.HasPrefix(line, "branch "):
			branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "":
			// A builder worktree is one on a bead/ branch that is not the repo
			// itself. Anything else belongs to a human.
			if wt != "" && wt != repo.Path && strings.HasPrefix(branch, "bead/") {
				found = append(found, Leftover{Repo: repo.Name, Worktree: wt, Branch: branch})
			}
			wt, branch = "", ""
		}
	}

	for i := range found {
		l := &found[i]
		l.Commits = commitsOn(repo.Path, l.Branch)
		if l.Commits > 0 {
			// Somebody's work. Detach the worktree so the ground is free, but
			// never touch the branch — destroying unmerged commits to tidy up
			// is worse than any mess it cleans.
			if err := git2(repo.Path, "worktree", "remove", "--force", l.Worktree); err != nil {
				l.Action, l.Detail = "failed", err.Error()
				continue
			}
			l.Action = "kept"
			l.Detail = fmt.Sprintf("%d commit(s) on %s — worktree detached, branch preserved for review", l.Commits, l.Branch)
			continue
		}
		if err := git2(repo.Path, "worktree", "remove", "--force", l.Worktree); err != nil {
			l.Action, l.Detail = "failed", err.Error()
			continue
		}
		if err := git2(repo.Path, "branch", "-D", l.Branch); err != nil {
			l.Action, l.Detail = "failed", "worktree removed but branch remains: "+err.Error()
			continue
		}
		l.Action, l.Detail = "swept", "no commits — worktree and branch removed, bead is allocatable again"
	}
	return found, nil
}

// commitsOn counts commits a branch carries over the repo's default branch.
// Unlike commitsSince this has no recorded base, because a stranded builder
// left no record of where it started.
func commitsOn(dir, branch string) int {
	out, err := inDir(dir, "git", "rev-list", "--count", "main.."+branch).Output()
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n
}

// ReleaseStaleSlots releases a merge slot left held by a builder that died.
// The slot has no TTL, so a single killed run makes every later builder in
// that repo burn the full acquire deadline for nothing.
func ReleaseStaleSlots(repo Repo) string {
	bd := bdClient{dir: repo.Path, run: execBD}
	out, err := bd.combined(repo.Path, "merge-slot", "check")
	if err != nil || !strings.Contains(strings.ToLower(string(out)), "held") {
		return ""
	}
	if _, err := bd.combined(repo.Path, "merge-slot", "release"); err != nil {
		return fmt.Sprintf("%s: merge slot held and could not be released: %v", repo.Name, err)
	}
	return fmt.Sprintf("%s: released a merge slot left held by a dead builder", repo.Name)
}
