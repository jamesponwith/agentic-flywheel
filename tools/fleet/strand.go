// A run killed outright — not a builder timing out, the coordinator itself —
// leaves a claimed bead behind with nothing to say it was ever claimed by the
// fleet. reclaim will not touch it: reclaim only sweeps EXPIRED LEASES, and a
// builder claims bare (`bd update --claim`, per the skill) — it never held a
// lease to expire. Nor should reclaim's rule change: "in progress with no
// lease" is also what a human's own in-flight work looks like, and that
// ambiguity is exactly what reclaim refuses to guess through.
//
// So the fleet leaves itself a note before spawning: which bead, on which
// branch, dispatched by which agent. A coordinator that exits normally clears
// it — see build()'s defer. A coordinator that is killed outright cannot run
// a defer, so the note survives on disk, and the NEXT run — before it
// allocates anything new — reads it and releases the bead, but only if
// nothing was ever committed. A branch with commits is somebody's work,
// possibly already in review, and this touches it no more than Reconcile or
// clearEmptyBranch do (fw-tf4).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const manifestFile = "run-manifest.json"

func manifestPath(repoPath string) string {
	return filepath.Join(repoPath, ".flywheel", manifestFile)
}

// runManifest records the one bead being dispatched to a repo. Only one entry
// ever exists at a time: BuildersPerRepo is one, always (run.go).
type runManifest struct {
	Bead    string    `json:"bead"`
	Branch  string    `json:"branch"`
	Agent   string    `json:"agent"`
	Started time.Time `json:"started"`
}

func writeManifest(repoPath string, m runManifest) error {
	if err := os.MkdirAll(filepath.Dir(manifestPath(repoPath)), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath(repoPath), append(b, '\n'), 0o644)
}

func clearManifest(repoPath string) error {
	err := os.Remove(manifestPath(repoPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readManifest(repoPath string) (runManifest, bool) {
	b, err := os.ReadFile(manifestPath(repoPath))
	if err != nil {
		return runManifest{}, false
	}
	var m runManifest
	if err := json.Unmarshal(b, &m); err != nil || m.Bead == "" {
		return runManifest{}, false
	}
	return m, true
}

// Stranded describes one bead ReleaseStranded gave back to the queue.
type Stranded struct {
	Bead   string `json:"bead"`
	Agent  string `json:"agent"`
	Detail string `json:"detail"`
}

// ReleaseStranded reads one repo's leftover manifest from a run that never
// cleared it and, if the bead it names is still in_progress with no commits
// on its branch, gives the bead back to the queue. Anything else — no
// manifest, a bead already closed or already reopened, a branch that carries
// real work — is left exactly alone.
//
// The manifest is cleared only once its bead is resolved one way or the
// other. A bd call that errors is NOT resolution — bd itself may just be
// down for a moment — and clearing on that error would erase the one record
// of the strand over an infra hiccup, recreating fw-tf4 through a different
// door. Left in place, the same manifest is simply tried again next run; the
// cost is a repeated warning, not a bead lost for good.
//
// bd is injected, not built from repo.Path, so a test can hand it an
// in-memory fake rather than shelling out to a real database (lease.go's
// leaser does the same).
func ReleaseStranded(repo Repo, bd bdClient) (*Stranded, error) {
	m, ok := readManifest(repo.Path)
	if !ok {
		return nil, nil
	}

	b, err := bd.show(m.Bead)
	if err != nil {
		return nil, err
	}
	if b.Status != "in_progress" {
		// Closed, or already back to open by some other path. Never reopen a
		// bead somebody moved on purpose.
		return nil, clearManifest(repo.Path)
	}
	// ponytail: main, not repo.DefaultBranch — DefaultBranch is resolved from
	// gh only inside reconcileBoards' own loop-local copy and never threaded
	// out (main.go), same ceiling Reconcile's commitsOn already lives with.
	// On a repo whose default branch isn't literally "main" this undercounts
	// to zero and a real branch could be misread as empty. Upgrade path:
	// resolve and persist repo.DefaultBranch once per roster load, then pass
	// it into commitsOn everywhere it's called instead of hardcoding "main".
	if n := commitsOn(repo.Path, repo.DefaultBranch, m.Branch); n > 0 {
		// Work exists, possibly already pushed and under review. Not this
		// function's call to touch.
		return nil, clearManifest(repo.Path)
	}

	md := copyMD(b.Metadata)
	delete(md, leaseHolderKey)
	delete(md, leaseExpiresKey)
	if err := bd.setMetadata(m.Bead, md); err != nil {
		return nil, err
	}
	if err := bd.reopen(m.Bead); err != nil {
		return nil, err
	}
	detail := fmt.Sprintf(
		"Builder %s claimed this bead, but the run that dispatched it was killed outright — the coordinator, not the "+
			"builder. No commits landed on %s. Returned to open (fw-tf4).", m.Agent, m.Branch)
	if err := bd.note(m.Bead, detail); err != nil {
		return nil, err
	}
	return &Stranded{Bead: m.Bead, Agent: m.Agent, Detail: detail}, clearManifest(repo.Path)
}
