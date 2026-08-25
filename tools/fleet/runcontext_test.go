package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readContext(t *testing.T, wt string) RunContext {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(wt, ".flywheel", "run-context.json"))
	if err != nil {
		t.Fatalf("no run context written: %v", err)
	}
	var rc RunContext
	if err := json.Unmarshal(b, &rc); err != nil {
		t.Fatalf("unreadable run context: %v", err)
	}
	return rc
}

func TestOneBuilderIsSolo(t *testing.T) {
	// With a single builder there is no second agent to contend with, so a
	// reservation prevents nothing — and requiring one has cost every run so
	// far: two consecutive no-ops at ~$2.50 each, neither of which touched a
	// file.
	wt := t.TempDir()
	if err := writeRunContext(wt, Assignment{Bead: "fw-1", Repo: "r"}, RunOpts{Concurrency: 1}); err != nil {
		t.Fatal(err)
	}
	rc := readContext(t, wt)
	if !rc.Solo {
		t.Error("one builder not reported as solo")
	}
	if rc.ConcurrentBuilders != 1 {
		t.Errorf("concurrency = %d, want 1 — the flag must carry its own reasoning", rc.ConcurrentBuilders)
	}
}

func TestMoreThanOneBuilderIsNotSolo(t *testing.T) {
	// The requirement has to come back on its own when concurrency rises.
	// A safety property that depends on someone remembering to flip a switch
	// is not one.
	for _, n := range []int{2, 3, 8} {
		wt := t.TempDir()
		if err := writeRunContext(wt, Assignment{Bead: "fw-1", Repo: "r"}, RunOpts{Concurrency: n}); err != nil {
			t.Fatal(err)
		}
		if rc := readContext(t, wt); rc.Solo {
			t.Errorf("concurrency %d reported as solo — two builders could edit the same file", n)
		}
	}
}

func TestRunContextCarriesNoCredentials(t *testing.T) {
	// It lands in a worktree and is readable by the model. The whole reason it
	// exists is that credentials must NOT travel this way — the alternative
	// proposed was a guard.sh subcommand that prints the token, which routes a
	// credential around the sandbox rule written to stop exactly that.
	wt := t.TempDir()
	t.Setenv("FLYWHEEL_BLACKBIRD_TOKEN", "super-secret-value")
	if err := writeRunContext(wt, Assignment{Bead: "fw-1", Repo: "r"}, RunOpts{Concurrency: 1}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wt, ".flywheel", "run-context.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"super-secret-value", "token", "TOKEN", "secret"} {
		if containsFold(string(b), banned) {
			t.Errorf("run context mentions %q:\n%s", banned, b)
		}
	}
}

func containsFold(hay, needle string) bool {
	return len(needle) > 0 && len(hay) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(hay); i++ {
				match := true
				for j := 0; j < len(needle); j++ {
					a, b := hay[i+j], needle[j]
					if a >= 'A' && a <= 'Z' {
						a += 32
					}
					if b >= 'A' && b <= 'Z' {
						b += 32
					}
					if a != b {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
			return false
		}()
}
