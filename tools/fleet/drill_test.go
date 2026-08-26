//go:build conformance

// Chaos drills (fw-oef.6).
//
// The router earned its credibility from a chaos harness that proved the
// resilience claims run rather than compile. The fleet deserves the same
// standard, and the parallel is worth naming: the flywheel applies its own
// pilot project's engineering bar to itself.
//
// Every drill here corresponds to something that actually went wrong during
// nine live builder runs. None is hypothetical.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// drillRepo is a scratch repo with a real bd database.
func drillRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// inDir, not a bare exec.Command. git exports GIT_DIR to its hooks, and a
	// `git config user.email` that inherits it writes to the REAL repository
	// rather than to this temp dir — permanently rebranding the maintainer as
	// the fixture identity. That is what happened here, and because the fixture
	// was "d@d" back then, GitHub attributed the commits to the stranger who
	// owns that address. The domain is the reserved TLD "invalid" now, so a
	// leak through some other hole attributes to nobody (fw-0rf).
	for _, a := range [][]string{{"init", "-q", "-b", "main"},
		{"config", "user.email", "d@invalid"}, {"config", "user.name", "d"}} {
		if out, err := inDir(dir, "git", a...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	_ = inDir(dir, "git", "commit", "-q", "--allow-empty", "-m", "root").Run()
	bdIn(t, dir, "init", "--prefix", "dr", "--non-interactive")
	return dir
}

// DRILL: a builder dies mid-bead. Its work must return to the queue.
func TestDrillKilledBuilderReturnsWorkToTheQueue(t *testing.T) {
	dir := drillRepo(t)
	id := bdIn(t, dir, "create", "work whose builder dies", "--silent")

	// Claim with an already-expired lease: indistinguishable from a builder
	// that died without heartbeating, which is what actually happens.
	if err := liveLeaser(dir, time.Now().Add(-2*time.Hour)).Claim(id, "fleet/builder-dead", time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err := liveLeaser(dir, time.Now()).Reclaim()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Holder != "fleet/builder-dead" {
		t.Fatalf("reclaim = %+v, want the dead builder's bead", got)
	}
	if !strings.Contains(bdIn(t, dir, "ready", "--json"), id) {
		t.Error("the bead did not return to the queue — the fleet would starve")
	}
}

// DRILL: the kill switch is thrown while builders are allocated.
func TestDrillKillSwitchStopsAnActiveCycle(t *testing.T) {
	dir := drillRepo(t)
	for i := 0; i < 4; i++ {
		bdIn(t, dir, "create", "queued", "--silent")
	}
	r := Roster{
		Caps:   Caps{ReviewWeightPerNight: 8, ConcurrentBuilders: 3, ReposPerNight: 1},
		Repos:  []Repo{{Name: "drill", Path: dir, Lang: "go"}},
		Agents: []Agent{{Name: "fleet/builder-go", Role: "builder", Repos: []string{"*"}}},
	}
	open := func(p string) bdClient { return bdClient{dir: p, run: execBD} }
	t.Setenv("FLYWHEEL_HOME", t.TempDir())

	before, err := Allocate(r, open, time.Now())
	if err != nil || len(before.Assignments) == 0 {
		t.Fatalf("nothing allocated to interrupt: %v %+v", err, before)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".flywheel", "STOP"), []byte("drill"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Allocate(r, open, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if after.Stopped == "" || len(after.Assignments) != 0 {
		t.Fatalf("allocated %d with the switch thrown", len(after.Assignments))
	}
}

// DRILL: a builder produces nothing. It must not be reported as success, and
// its bead must not be left stranded — both of which happened for real.
func TestDrillNoOpBuilderIsNotGreenAndDoesNotStrand(t *testing.T) {
	dir := drillRepo(t)
	id := bdIn(t, dir, "create", "work nobody does", "--silent")
	bdIn(t, dir, "update", id, "--claim")

	// No commits: whatever the exit code, this is not green.
	base := headOf(dir)
	if n := commitsSince(dir, base, "main"); n != 0 {
		t.Fatalf("counted %d commits where the builder made none", n)
	}
	// And an unleased in-progress bead is left for a human, never reclaimed.
	got, err := liveLeaser(dir, time.Now()).Reclaim()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("reclaimed %+v — an unleased in-progress bead may be a human's work", got)
	}
}

// DRILL: the account hits its usage limit. The fleet must hold, say until
// when, and come back on its own.
//
// This drill used to exhaust a per-repo dollar budget. That budget is gone —
// a subscription bills no marginal dollar and states no remaining balance, so
// the number was never checkable. The limit that actually stops the fleet is
// the account's, and the drill now exercises that: the real 429 message, the
// hold it produces, and the recovery nobody has to trigger.
func TestDrillAccountLimitHoldsThenReleases(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	reset, err := noteRateLimit(dir,
		"You've hit your session limit \u00b7 resets 6pm (America/Los_Angeles)", now)
	if err != nil {
		t.Fatal(err)
	}
	if !reset.After(now) {
		t.Fatalf("reset %s is not in the future; the fleet would dispatch straight back into the wall", reset)
	}

	until, why, held := heldUntil(dir, now)
	if !held {
		t.Fatal("the account said stop and nothing recorded it")
	}
	if why == "" {
		t.Error("held without saying why — an operator finding an idle fleet has nothing to read")
	}
	if !until.Equal(reset) {
		t.Errorf("hold until %s, want %s", until, reset)
	}

	// And it must lift itself. A hold that needs a human to clear it turns a
	// scheduled pause into an outage.
	if _, _, still := heldUntil(dir, reset.Add(time.Minute)); still {
		t.Error("still held after the account said the quota returned")
	}
}
