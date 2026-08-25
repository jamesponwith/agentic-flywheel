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

// The two solo tests that lived here asserted "solo" from the FLEET-wide
// concurrency. That bound was wrong in the safe direction and cost throughput:
// blackbird keys reservations by project_key, so builders in different repos
// cannot collide however many run. Solo is per-repo now, and the cases moved
// to fleetwidth_test.go where the width they interact with is also tested.

func TestRunContextCarriesNoCredentials(t *testing.T) {
	// It lands in a worktree and is readable by the model. The whole reason it
	// exists is that credentials must NOT travel this way — the alternative
	// proposed was a guard.sh subcommand that prints the token, which routes a
	// credential around the sandbox rule written to stop exactly that.
	wt := t.TempDir()
	t.Setenv("FLYWHEEL_BLACKBIRD_TOKEN", "super-secret-value")
	if err := writeRunContext(wt, Assignment{Bead: "fw-1", Repo: "r"}, RunOpts{}, 1, 1); err != nil {
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
