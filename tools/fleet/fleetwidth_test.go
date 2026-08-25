package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func ctxOf(t *testing.T, wt string) RunContext {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(wt, ".flywheel", "run-context.json"))
	if err != nil {
		t.Fatalf("no run context: %v", err)
	}
	var rc RunContext
	if err := json.Unmarshal(b, &rc); err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestWidthFollowsTheWorkNotTheCeiling(t *testing.T) {
	// Spawning more builders than there are repos with work is pure startup
	// cost — and startup is what the last three runs paid, ~$2.50 each, to
	// reach a wall. The ceiling bounds; the workload sizes.
	cases := []struct {
		name    string
		repos   []string
		ceiling int
		want    int
	}{
		{"one repo, generous ceiling", []string{"a"}, 5, 1},
		{"three repos, generous ceiling", []string{"a", "b", "c"}, 5, 3},
		{"three repos, ceiling of two", []string{"a", "b", "c"}, 2, 2},
		{"two beads in ONE repo is still one builder wide", []string{"a", "a"}, 5, 1},
		{"no ceiling set still runs", []string{"a", "b"}, 0, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var as []Assignment
			for i, r := range tc.repos {
				as = append(as, Assignment{Bead: string(rune('a' + i)), Repo: r})
			}
			if got := fleetWidth(as, tc.ceiling); got != tc.want {
				t.Errorf("width = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestEveryBuilderIsSoloInItsOwnRepo(t *testing.T) {
	// The bound that matters. blackbird keys reservations by project_key, so
	// builders in different repos cannot collide however many run — and one
	// builder per repo is what keeps each of them solo, so widening the fleet
	// never re-arms the reservation requirement that cost three runs (fw-k8f).
	wt := t.TempDir()
	if err := writeRunContext(wt, Assignment{Bead: "fw-1", Repo: "a"},
		RunOpts{}, 1, 5); err != nil {
		t.Fatal(err)
	}
	rc := ctxOf(t, wt)
	if !rc.Solo {
		t.Error("a builder alone in its repo was not reported solo")
	}
	if rc.FleetWidth != 5 {
		t.Errorf("fleet width = %d, want 5 reported", rc.FleetWidth)
	}
	if rc.BuildersInThisRepo != 1 {
		t.Errorf("builders in repo = %d, want 1", rc.BuildersInThisRepo)
	}
}

func TestTwoBuildersInOneRepoIsNotSolo(t *testing.T) {
	// If per-repo concurrency ever rises, the reservation requirement has to
	// come back by itself rather than by someone remembering.
	wt := t.TempDir()
	if err := writeRunContext(wt, Assignment{Bead: "fw-1", Repo: "a"},
		RunOpts{}, 2, 5); err != nil {
		t.Fatal(err)
	}
	if ctxOf(t, wt).Solo {
		t.Error("two builders in one repo reported as solo — they can edit the same file")
	}
}
