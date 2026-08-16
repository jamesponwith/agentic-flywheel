// Hydrate: make a repo's beads readable before the coordinator asks it for work.
//
// Found the hard way (fw-oef.8): the first real `fleet allocate` across all five
// repos declined llm-resiliency-router with "no beads database found". Its
// .beads/issues.jsonl is committed, but the Dolt database is gitignored — so a
// freshly cloned instance has beads on disk that bd cannot read, and the
// coordinator sees an empty queue where there is real work.
//
// The upstream guidance is to sync via `bd dolt pull` and to avoid `bd import`
// during normal operation. That is right, and it does not apply here: the
// remotes carry no refs/dolt/* at all, so the committed JSONL is the only
// durable copy. This is the bootstrap-a-missing-database case import exists for,
// and it runs once per clone, not per cycle.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type HydrateResult struct {
	Repo   string `json:"repo"`
	Action string `json:"action"` // ok | hydrated | skipped | failed
	Detail string `json:"detail,omitempty"`
}

// hydrateOps are the three side effects, injected so tests never create real
// databases.
type hydrateOps struct {
	readable func(path string) error // can bd read this repo's beads?
	initDB   func(path string) error
	importDB func(path, jsonl string) error
	// dirty reports tracked files modified under .beads/.
	dirty func(path string) []string
	// head reports the current commit. Checked before and after, because the
	// dirty check alone cannot see this: `bd init` rewrites CLAUDE.md,
	// AGENTS.md and settings.json, and beads installs git hooks that COMMIT
	// those changes — so by the time we look for a dirty tree there is none.
	//
	// This happened twice for real (fw-oef.12): both llm-resiliency-router and
	// brax-tennis-rl gained an uninvited "bd init" commit, and one of them
	// reverted a merged PR's changes. The second reached an open PR and passed
	// CI, because nothing was broken — it simply was not what the PR claimed.
	head func(path string) string
}

func liveHydrateOps() hydrateOps {
	return hydrateOps{
		readable: func(p string) error {
			_, err := execBD(p, "ready", "--json")
			return err
		},
		initDB: func(p string) error {
			_, err := execBD(p, "init", "--non-interactive")
			return err
		},
		importDB: func(p, jsonl string) error {
			_, err := execBD(p, "import", jsonl)
			return err
		},
		dirty: gitDirtyBeads,
		head:  gitHead,
	}
}

// gitHead returns the current commit, or "" if this is not a git repo.
func gitHead(path string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitDirtyBeads lists tracked files modified under .beads/ in a repo.
func gitDirtyBeads(path string) []string {
	cmd := exec.Command("git", "status", "--porcelain", "--", ".beads")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || strings.HasPrefix(line, "??") {
			continue // untracked is fine: the database itself is gitignored
		}
		if f := strings.TrimSpace(line[2:]); f != "" {
			files = append(files, f)
		}
	}
	return files
}

// Hydrate brings every roster repo to a readable state. It is idempotent: a
// repo whose beads already read is left completely alone, so this is safe to
// run before every cycle.
//
// It never deletes or overwrites an existing database. The worst case is a
// repo it cannot fix, which it reports rather than hiding — the coordinator
// declining a repo out loud is the behaviour that surfaced this bug.
func Hydrate(r Roster, ops hydrateOps) []HydrateResult {
	var out []HydrateResult
	for _, repo := range r.Repos {
		// A paused repo will not be allocated work, so its database being cold
		// is not a problem to report. Reporting it would make every scheduled
		// run look broken, and an alert that is always red is not an alert.
		if repo.Paused {
			out = append(out, HydrateResult{Repo: repo.Name, Action: "paused"})
			continue
		}
		if err := ops.readable(repo.Path); err == nil {
			out = append(out, HydrateResult{Repo: repo.Name, Action: "ok"})
			continue
		}

		jsonl := filepath.Join(repo.Path, ".beads", "issues.jsonl")
		if _, err := os.Stat(jsonl); err != nil {
			out = append(out, HydrateResult{Repo: repo.Name, Action: "skipped",
				Detail: "unreadable and no .beads/issues.jsonl to restore from"})
			continue
		}

		// Record HEAD before touching anything, so a commit made by bd's own
		// git hooks is detectable afterwards.
		before := ""
		if ops.head != nil {
			before = ops.head(repo.Path)
		}

		if err := ops.initDB(repo.Path); err != nil {
			out = append(out, HydrateResult{Repo: repo.Name, Action: "failed", Detail: "bd init: " + err.Error()})
			continue
		}
		if err := ops.importDB(repo.Path, jsonl); err != nil {
			out = append(out, HydrateResult{Repo: repo.Name, Action: "failed", Detail: "bd import: " + err.Error()})
			continue
		}
		// Only claim success if bd can actually read it now. "I ran the
		// commands" is not the same as "it works".
		if err := ops.readable(repo.Path); err != nil {
			out = append(out, HydrateResult{Repo: repo.Name, Action: "failed",
				Detail: "still unreadable after import: " + err.Error()})
			continue
		}
		detail := "restored from " + jsonl
		if ops.dirty != nil {
			if files := ops.dirty(repo.Path); len(files) > 0 {
				detail += fmt.Sprintf("; WARNING left tracked files modified: %s", strings.Join(files, ", "))
			}
		}
		// The important check. A commit we did not ask for is a repository
		// mutation, and it is silent: it survives CI and contaminates any
		// branch cut afterwards.
		if ops.head != nil && before != "" {
			if after := ops.head(repo.Path); after != "" && after != before {
				out = append(out, HydrateResult{Repo: repo.Name, Action: "mutated",
					Detail: fmt.Sprintf("bd init COMMITTED to this repo: %s -> %s. "+
						"Inspect and reset before cutting any branch — `git log %s..HEAD`",
						before[:min(8, len(before))], after[:min(8, len(after))], before[:min(8, len(before))])})
				continue
			}
		}
		out = append(out, HydrateResult{Repo: repo.Name, Action: "hydrated", Detail: detail})
	}
	return out
}

func summarize(rs []HydrateResult) string {
	var ok, hydrated, paused, bad int
	for _, r := range rs {
		switch r.Action {
		case "ok":
			ok++
		case "hydrated":
			hydrated++
		case "paused":
			paused++
		default:
			bad++
		}
	}
	return fmt.Sprintf("%d readable, %d hydrated, %d paused, %d needing attention", ok, hydrated, paused, bad)
}
