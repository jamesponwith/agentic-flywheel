// Merge serialisation (fw-lb8.4).
//
// Worktrees isolate edits; they do not isolate merges. Two builders can each be
// green in isolation and still produce an incoherent main — the classic case
// being two branches that both rewrite the same file's imports, each passing
// its own gate. bd ships a merge slot for exactly this: an exclusive primitive
// only one holder at a time, with a priority-ordered queue of waiters.
//
// The slot is taken around the PUSH, not around the build. Builds are the slow
// part and are genuinely independent; only the moment a branch reaches the
// shared remote needs serialising.
package main

import (
	"fmt"
	"strings"
	"time"
)

// mergeSlot wraps bd's merge-slot for one repo.
type mergeSlot struct {
	bd bdClient
}

// ensure creates the slot bead if the rig has none. Idempotent — bd refuses a
// second create, and that refusal is not an error worth surfacing.
func (m mergeSlot) ensure() {
	_, _ = m.bd.run(m.bd.dir, "merge-slot", "create")
}

// acquire blocks until the slot is free or the deadline passes. It returns
// whether the slot is held: a builder that cannot get it must not push, because
// pushing anyway is precisely the race the slot exists to prevent.
func (m mergeSlot) acquire(within time.Duration, now func() time.Time) (bool, string) {
	deadline := now().Add(within)
	for {
		out, err := m.bd.run(m.bd.dir, "merge-slot", "acquire")
		if err == nil && !strings.Contains(strings.ToLower(string(out)), "held") {
			return true, ""
		}
		if now().After(deadline) {
			return false, fmt.Sprintf("merge slot busy for %s; branch pushed by nobody — rerun or merge by hand", within)
		}
		time.Sleep(2 * time.Second)
	}
}

func (m mergeSlot) release() {
	_, _ = m.bd.run(m.bd.dir, "merge-slot", "release")
}
