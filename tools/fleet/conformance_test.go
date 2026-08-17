//go:build conformance

// The conformance harness. Every claim the fleet makes is a claim about
// behaviour under failure, and failure is exactly what does not get exercised
// by things going fine — so this runs against a REAL bd database in a scratch
// repo, not against the in-memory fake.
//
//	go test -tags=conformance ./tools/fleet/
//
// Excluded from the ordinary suite because it needs the bd binary and writes a
// database. Reservation conformance (blackbird territory) is not here: the
// reservation API is MCP-only, so it is driven by an agent — see fw-wb2.7.
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bdIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("bd", args...)
	cmd.Dir = dir
	cmd.Env = hermeticEnv() // bd is a git client too
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// scratchRepo builds a throwaway git repo with a real beads database.
func scratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "conformance@test"}, {"config", "user.name", "conformance"},
	} {
		if out, err := gitCmd(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	bdIn(t, dir, "init", "--prefix", "cf", "--non-interactive")
	return dir
}

func newBead(t *testing.T, dir, title string, extra ...string) string {
	t.Helper()
	return bdIn(t, dir, append([]string{"create", title, "--silent"}, extra...)...)
}

func liveLeaser(dir string, now time.Time) leaser {
	return leaser{bd: bdClient{dir: dir, run: execBD}, now: func() time.Time { return now }}
}

// TestConformanceTwoAgentsOneBead: the core protocol property. Two builders
// must never hold the same bead.
func TestConformanceTwoAgentsOneBead(t *testing.T) {
	dir := scratchRepo(t)
	id := newBead(t, dir, "contested work")
	now := time.Now()

	if err := liveLeaser(dir, now).Claim(id, "fleet/builder-a", time.Hour); err != nil {
		t.Fatalf("first claim failed: %v", err)
	}
	err := liveLeaser(dir, now).Claim(id, "fleet/builder-b", time.Hour)
	if err == nil {
		t.Fatal("second agent claimed a bead already under a live lease")
	}
	if !strings.Contains(err.Error(), "held by fleet/builder-a") {
		t.Errorf("refusal does not name the holder: %v", err)
	}
}

// TestConformanceKilledAgentIsReclaimed: an agent that dies mid-bead must not
// starve the fleet forever.
func TestConformanceKilledAgentIsReclaimed(t *testing.T) {
	dir := scratchRepo(t)
	id := newBead(t, dir, "work whose agent dies")

	// Claim with a lease that has already expired: the agent died without
	// heartbeating, which is indistinguishable from this state.
	if err := liveLeaser(dir, time.Now().Add(-2*time.Hour)).Claim(id, "fleet/builder-dead", time.Hour); err != nil {
		t.Fatal(err)
	}

	got, err := liveLeaser(dir, time.Now()).Reclaim()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id || got[0].Holder != "fleet/builder-dead" {
		t.Fatalf("reclaim = %+v, want the dead agent's bead", got)
	}

	// It must be genuinely re-allocatable, not merely re-labelled.
	ready := bdIn(t, dir, "ready", "--json")
	if !strings.Contains(ready, id) {
		t.Errorf("%s is not back in bd ready after reclaim:\n%s", id, ready)
	}
	if err := liveLeaser(dir, time.Now()).Claim(id, "fleet/builder-replacement", time.Hour); err != nil {
		t.Errorf("replacement could not claim the reclaimed bead: %v", err)
	}

	// And the next agent must be told what happened.
	show := bdIn(t, dir, "show", id)
	if !strings.Contains(show, "Lease reclaimed") || !strings.Contains(show, "fleet/builder-dead") {
		t.Errorf("no note naming who dropped it; next agent will not know about the stale worktree:\n%s", show)
	}
}

// TestConformanceStaleAgentCannotResurrect: the reclaimed agent wakes up and
// heartbeats. It must be refused, or two agents edit the same ground.
func TestConformanceStaleAgentCannotResurrect(t *testing.T) {
	dir := scratchRepo(t)
	id := newBead(t, dir, "work that changes hands")

	if err := liveLeaser(dir, time.Now().Add(-2*time.Hour)).Claim(id, "fleet/builder-dead", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := liveLeaser(dir, time.Now()).Reclaim(); err != nil {
		t.Fatal(err)
	}
	if err := liveLeaser(dir, time.Now()).Claim(id, "fleet/builder-new", time.Hour); err != nil {
		t.Fatal(err)
	}
	err := liveLeaser(dir, time.Now()).Heartbeat(id, "fleet/builder-dead", time.Hour)
	if err == nil {
		t.Fatal("the reclaimed agent resurrected its lease alongside its replacement")
	}
	if !strings.Contains(err.Error(), "stop working") {
		t.Errorf("refusal does not tell the stale agent to stop: %v", err)
	}
}

// TestConformanceHumanWorkIsNeverReclaimed: an in-progress bead with no lease
// belongs to a person. Taking it would be worse than the starvation we prevent.
func TestConformanceHumanWorkIsNeverReclaimed(t *testing.T) {
	dir := scratchRepo(t)
	id := newBead(t, dir, "a human is working this")
	bdIn(t, dir, "update", id, "--claim")

	got, err := liveLeaser(dir, time.Now()).Reclaim()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("reclaimed %+v — that is a human's in-progress work", got)
	}
}

// TestConformanceKillSwitchHaltsMidCycle: three agents active, switch thrown,
// nothing further allocated.
func TestConformanceKillSwitchHaltsMidCycle(t *testing.T) {
	dir := scratchRepo(t)
	for i := 0; i < 4; i++ {
		newBead(t, dir, "queued work")
	}
	r := Roster{
		Caps:   Caps{PRsPerNight: 6, ConcurrentBuilders: 3, ReposPerNight: 2},
		Repos:  []Repo{{Name: "scratch", Path: dir, Lang: "go"}},
		Agents: []Agent{{Name: "fleet/builder-go", Role: "builder", Repos: []string{"*"}, Skills: []string{"go"}}},
	}
	open := func(p string) bdClient { return bdClient{dir: p, run: execBD} }
	t.Setenv("FLYWHEEL_HOME", t.TempDir())

	before, err := Allocate(r, open, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Assignments) != 3 {
		t.Fatalf("allocated %d, want 3 (the concurrent-builder cap)", len(before.Assignments))
	}

	if err := os.MkdirAll(filepath.Join(dir, ".flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".flywheel", "STOP"), []byte("conformance drill"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Allocate(r, open, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if after.Stopped == "" || len(after.Assignments) != 0 {
		t.Fatalf("cycle allocated %d beads with the kill switch set (stopped=%q)", len(after.Assignments), after.Stopped)
	}
}

// TestConformanceCapsAreNeverExceeded against a real queue.
func TestConformanceCapsAreNeverExceeded(t *testing.T) {
	dir := scratchRepo(t)
	for i := 0; i < 12; i++ {
		newBead(t, dir, "queued work")
	}
	r := Roster{
		Caps:   Caps{PRsPerNight: 2, ConcurrentBuilders: 5, ReposPerNight: 1},
		Repos:  []Repo{{Name: "scratch", Path: dir, Lang: "go"}},
		Agents: []Agent{{Name: "fleet/builder-go", Role: "builder", Repos: []string{"*"}}},
	}
	t.Setenv("FLYWHEEL_HOME", t.TempDir())
	plan, err := Allocate(r, func(p string) bdClient { return bdClient{dir: p, run: execBD} }, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Assignments) > 2 {
		t.Fatalf("allocated %d PRs against a cap of 2", len(plan.Assignments))
	}
	if len(plan.Declined) == 0 {
		t.Error("hit the cap and declined nothing — silent truncation reads as 'nothing to do'")
	}
	// The plan must serialise: the digest and the audit trail depend on it.
	if _, err := json.Marshal(plan); err != nil {
		t.Fatal(err)
	}
}
