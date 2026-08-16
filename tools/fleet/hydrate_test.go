package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hydrateFixture builds a roster of temp repos, marking which ones bd can read
// and which have a committed issues.jsonl to restore from.
func hydrateFixture(t *testing.T, repos map[string][2]bool) (Roster, map[string]string) {
	t.Helper()
	var r Roster
	r.Caps = Caps{PRsPerNight: 1, ConcurrentBuilders: 1, ReposPerNight: 1}
	paths := map[string]string{}
	for name, flags := range repos {
		dir := t.TempDir()
		paths[name] = dir
		if hasJSONL := flags[1]; hasJSONL {
			if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		r.Repos = append(r.Repos, Repo{Name: name, Path: dir})
	}
	return r, paths
}

func TestHydrate(t *testing.T) {
	// readable[path] flips to true once initDB+importDB have run, modelling a
	// database that genuinely appears.
	tests := []struct {
		name       string
		repos      map[string][2]bool // name -> {readableNow, hasJSONL}
		importFail bool
		stillBroke bool
		want       map[string]string // repo -> action
		wantCalls  map[string]bool   // which repos got touched
	}{
		{
			name:      "already readable repos are left completely alone",
			repos:     map[string][2]bool{"fine": {true, true}},
			want:      map[string]string{"fine": "ok"},
			wantCalls: map[string]bool{},
		},
		{
			name:      "unreadable repo with a committed jsonl is restored",
			repos:     map[string][2]bool{"stale": {false, true}},
			want:      map[string]string{"stale": "hydrated"},
			wantCalls: map[string]bool{"stale": true},
		},
		{
			name:      "unreadable repo with nothing to restore from is reported, not hidden",
			repos:     map[string][2]bool{"empty": {false, false}},
			want:      map[string]string{"empty": "skipped"},
			wantCalls: map[string]bool{},
		},
		{
			name:       "a failed import is reported as failed",
			repos:      map[string][2]bool{"broken": {false, true}},
			importFail: true,
			want:       map[string]string{"broken": "failed"},
			wantCalls:  map[string]bool{"broken": true},
		},
		{
			name:       "commands that ran but did not work are not called success",
			repos:      map[string][2]bool{"liar": {false, true}},
			stillBroke: true,
			want:       map[string]string{"liar": "failed"},
			wantCalls:  map[string]bool{"liar": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, paths := hydrateFixture(t, tt.repos)
			readable := map[string]bool{}
			for name, flags := range tt.repos {
				readable[paths[name]] = flags[0]
			}
			touched := map[string]bool{}
			nameOf := func(p string) string {
				for n, d := range paths {
					if d == p {
						return n
					}
				}
				return p
			}

			ops := hydrateOps{
				head: func(string) string { return "same" }, // no commit made
				readable: func(p string) error {
					if readable[p] {
						return nil
					}
					return errNotFound{"no beads database found"}
				},
				initDB: func(p string) error {
					touched[nameOf(p)] = true
					return nil
				},
				importDB: func(p, jsonl string) error {
					touched[nameOf(p)] = true
					if tt.importFail {
						return errNotFound{"import blew up"}
					}
					if !tt.stillBroke {
						readable[p] = true
					}
					return nil
				},
			}

			got := Hydrate(r, ops)
			for _, res := range got {
				if want := tt.want[res.Repo]; res.Action != want {
					t.Errorf("%s: action = %q (%s), want %q", res.Repo, res.Action, res.Detail, want)
				}
			}
			for name := range tt.repos {
				if touched[name] != tt.wantCalls[name] {
					t.Errorf("%s: touched = %v, want %v", name, touched[name], tt.wantCalls[name])
				}
			}
		})
	}
}

func TestHydrateIsIdempotent(t *testing.T) {
	// Safe to run before every cycle: the second pass must do nothing at all.
	r, paths := hydrateFixture(t, map[string][2]bool{"repo": {false, true}})
	readable := map[string]bool{paths["repo"]: false}
	inits := 0
	ops := hydrateOps{
		readable: func(p string) error {
			if readable[p] {
				return nil
			}
			return errNotFound{"nope"}
		},
		initDB:   func(p string) error { inits++; return nil },
		importDB: func(p, _ string) error { readable[p] = true; return nil },
	}
	if got := Hydrate(r, ops); got[0].Action != "hydrated" {
		t.Fatalf("first pass = %q, want hydrated", got[0].Action)
	}
	if got := Hydrate(r, ops); got[0].Action != "ok" {
		t.Fatalf("second pass = %q, want ok", got[0].Action)
	}
	if inits != 1 {
		t.Errorf("bd init ran %d times, want 1 — hydrate is not idempotent", inits)
	}
}

func TestSummarize(t *testing.T) {
	got := summarize([]HydrateResult{{Action: "ok"}, {Action: "hydrated"}, {Action: "failed"}, {Action: "skipped"}})
	if !strings.Contains(got, "1 readable") || !strings.Contains(got, "1 hydrated") || !strings.Contains(got, "2 needing attention") {
		t.Errorf("summary = %q", got)
	}
}

func TestHydrateSkipsPausedRepos(t *testing.T) {
	// A paused repo gets no work, so a cold database there is not a fault.
	// Counting it as one would make every scheduled run look broken.
	r, _ := hydrateFixture(t, map[string][2]bool{"dormant": {false, false}})
	r.Repos[0].Paused = true
	touched := false
	got := Hydrate(r, hydrateOps{
		readable: func(string) error { t.Error("probed a paused repo"); return nil },
		initDB:   func(string) error { touched = true; return nil },
		importDB: func(string, string) error { touched = true; return nil },
	})
	if got[0].Action != "paused" {
		t.Errorf("action = %q, want paused", got[0].Action)
	}
	if touched {
		t.Error("touched a paused repo")
	}
	if strings.Contains(summarize(got), "1 needing attention") {
		t.Error("a paused repo was counted as needing attention")
	}
}

func TestHydrateWarnsWhenItDirtiesTrackedFiles(t *testing.T) {
	// bd init writes sync.remote into config.yaml, which some repos track.
	// Hydrating must never do that silently.
	r, paths := hydrateFixture(t, map[string][2]bool{"repo": {false, true}})
	readable := map[string]bool{paths["repo"]: false}
	got := Hydrate(r, hydrateOps{
		readable: func(p string) error {
			if readable[p] {
				return nil
			}
			return errNotFound{"nope"}
		},
		initDB:   func(string) error { return nil },
		importDB: func(p, _ string) error { readable[p] = true; return nil },
		dirty:    func(string) []string { return []string{".beads/config.yaml"} },
	})
	if got[0].Action != "hydrated" {
		t.Fatalf("action = %q", got[0].Action)
	}
	if !strings.Contains(got[0].Detail, "WARNING") || !strings.Contains(got[0].Detail, "config.yaml") {
		t.Errorf("dirtied a tracked file without saying so: %q", got[0].Detail)
	}
}

func TestHydrateDetectsACommitItCaused(t *testing.T) {
	// The fw-oef.12 bug. `bd init` rewrites CLAUDE.md/AGENTS.md and beads' git
	// hooks commit them, so the dirty check sees a clean tree and says nothing.
	// It happened twice for real; once it reverted a merged PR, and once it
	// reached an open PR and passed CI.
	r, paths := hydrateFixture(t, map[string][2]bool{"repo": {false, true}})
	readable := map[string]bool{paths["repo"]: false}
	head := "aaaaaaaaaaaa"

	got := Hydrate(r, hydrateOps{
		readable: func(p string) error {
			if readable[p] {
				return nil
			}
			return errNotFound{"nope"}
		},
		initDB: func(p string) error {
			head = "bbbbbbbbbbbb" // beads' hooks commit during init
			return nil
		},
		importDB: func(p, _ string) error { readable[p] = true; return nil },
		dirty:    func(string) []string { return nil }, // tree is clean: hooks committed
		head:     func(string) string { return head },
	})

	if got[0].Action != "mutated" {
		t.Fatalf("action = %q, want mutated — a commit we did not ask for is not a success", got[0].Action)
	}
	if !strings.Contains(got[0].Detail, "COMMITTED") || !strings.Contains(got[0].Detail, "aaaaaaaa") {
		t.Errorf("detail does not name the commit range: %q", got[0].Detail)
	}
	if strings.Contains(summarize(got), "1 hydrated") {
		t.Error("a repo that got mutated was counted as cleanly hydrated")
	}
	if !strings.Contains(summarize(got), "1 needing attention") {
		t.Errorf("mutation not counted as needing attention: %q", summarize(got))
	}
}

func TestHydrateStaysQuietWhenHeadDoesNotMove(t *testing.T) {
	// The ordinary case must not become noisy: a hydrate that only creates the
	// gitignored database changes no commit and should report plain success.
	r, paths := hydrateFixture(t, map[string][2]bool{"repo": {false, true}})
	readable := map[string]bool{paths["repo"]: false}
	got := Hydrate(r, hydrateOps{
		readable: func(p string) error {
			if readable[p] {
				return nil
			}
			return errNotFound{"nope"}
		},
		initDB:   func(string) error { return nil },
		importDB: func(p, _ string) error { readable[p] = true; return nil },
		dirty:    func(string) []string { return nil },
		head:     func(string) string { return "unchanged" },
	})
	if got[0].Action != "hydrated" {
		t.Errorf("action = %q, want hydrated", got[0].Action)
	}
}

func TestHydrateHandlesNonGitDirectories(t *testing.T) {
	// gitHead returns "" outside a git repo; that must not read as a mutation.
	r, paths := hydrateFixture(t, map[string][2]bool{"repo": {false, true}})
	readable := map[string]bool{paths["repo"]: false}
	got := Hydrate(r, hydrateOps{
		readable: func(p string) error {
			if readable[p] {
				return nil
			}
			return errNotFound{"nope"}
		},
		initDB:   func(string) error { return nil },
		importDB: func(p, _ string) error { readable[p] = true; return nil },
		dirty:    func(string) []string { return nil },
		head:     func(string) string { return "" },
	})
	if got[0].Action != "hydrated" {
		t.Errorf("action = %q, want hydrated — a non-git dir is not a mutation", got[0].Action)
	}
}
