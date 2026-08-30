package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// holdIn plants a quota hold in a scratch repo and returns it as a Repo.
func holdIn(t *testing.T, name string, until time.Time) Repo {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !until.IsZero() {
		if err := holdUntil(dir, until, "session limit ("+name+")"); err != nil {
			t.Fatal(err)
		}
	}
	return Repo{Name: name, Path: dir}
}

func TestEarliestHoldLooksInEveryRepo(t *testing.T) {
	// The bug: noteRateLimit writes the hold into the repo whose builder hit
	// the wall, and nightly.sh read only its OWN repo's file. A Spotify
	// builder's 429 landed in Spotify; the flywheel's copy still held a
	// two-day-old expired hold, so the delay came out negative and no resume
	// was scheduled. The fleet sat until a person noticed.
	now := time.Now()
	r := Roster{Repos: []Repo{
		holdIn(t, "flywheel", time.Time{}),         // no hold here — the repo doing the asking
		holdIn(t, "spotify", now.Add(2*time.Hour)), // the one that actually hit the wall
	}}
	until, why, held := earliestHold(r, now)
	if !held {
		t.Fatal("found no hold; the resume would never be scheduled")
	}
	if !until.After(now) {
		t.Errorf("hold %s is not in the future", until)
	}
	if why == "" {
		t.Error("no reason carried; an operator finding an idle fleet has nothing to read")
	}
}

func TestEarliestHoldWins(t *testing.T) {
	// The account is one pool, so work can resume at the FIRST reset.
	now := time.Now()
	soon := now.Add(30 * time.Minute)
	r := Roster{Repos: []Repo{
		holdIn(t, "late", now.Add(5*time.Hour)),
		holdIn(t, "soon", soon),
		holdIn(t, "later", now.Add(9*time.Hour)),
	}}
	until, _, held := earliestHold(r, now)
	if !held {
		t.Fatal("no hold found")
	}
	if until.Sub(soon).Abs() > time.Second {
		t.Errorf("chose %s, want the earliest (%s)", until, soon)
	}
}

func TestAnExpiredHoldIsNotAHold(t *testing.T) {
	// Exactly what was on disk: a two-day-old hold. Reading it as current is
	// how the delay came out negative.
	now := time.Now()
	r := Roster{Repos: []Repo{holdIn(t, "stale", now.Add(-48*time.Hour))}}
	if _, _, held := earliestHold(r, now); held {
		t.Error("an expired hold was reported as active")
	}
}

func TestNoHoldsAnywhereMeansRun(t *testing.T) {
	r := Roster{Repos: []Repo{holdIn(t, "a", time.Time{}), holdIn(t, "b", time.Time{})}}
	if _, _, held := earliestHold(r, time.Now()); held {
		t.Error("invented a hold; the fleet would never start")
	}
}
