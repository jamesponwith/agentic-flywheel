package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRunReport(t *testing.T) {
	// One report, written five ways. Every one carries the same cost, so every
	// one must parse: an earlier version anchored on the literal `{"is_error"`
	// and silently required that key to come first, which left real runs
	// unmeasured while its own fixture passed.
	const usage = `"usage":{"input_tokens":2,"cache_creation_input_tokens":6822,"cache_read_input_tokens":16016,"output_tokens":4}`

	tests := []struct {
		name    string
		in      string
		wantOK  bool
		wantUSD float64
		wantTok int
	}{
		{"is_error first, after prose",
			"Some prose the agent wrote first.\n" + `{"is_error":false,"num_turns":7,"total_cost_usd":0.0772740,` + usage + `}`,
			true, 0.077274, 22844},
		{"type first, is_error buried mid-object",
			`{"type":"result","subtype":"success","is_error":false,"num_turns":7,"total_cost_usd":0.0772740,` + usage + `}`,
			true, 0.077274, 22844},
		{"cost last, after the nested usage object",
			`{"type":"result",` + usage + `,"num_turns":7,"total_cost_usd":0.0772740}`,
			true, 0.077274, 22844},
		{"pretty-printed",
			"{\n  \"is_error\": false,\n  \"num_turns\": 7,\n  \"total_cost_usd\": 0.0772740,\n  " + usage + "\n}",
			true, 0.077274, 22844},
		{"a stray brace after the report",
			`{"is_error":false,"num_turns":7,"total_cost_usd":0.0772740,` + usage + `}}`,
			true, 0.077274, 22844},
		{"two reports: the last one wins",
			`{"total_cost_usd":1.5,"num_turns":1}` + "\n" + `{"num_turns":7,"total_cost_usd":0.0772740,` + usage + `}`,
			true, 0.077274, 22844},

		{"no JSON at all leaves the run unmeasured", "I stopped at step 4.", false, 0, 0},
		{"a real abandoned run", "Error: Reached max turns (60)", false, 0, 0},
		{"malformed JSON leaves it unmeasured", `{"total_cost_usd":`, false, 0, 0},
		// The distinction cost.go exists for: a report carrying no cost is
		// unmeasured, not free. Returning ok here would publish $0.00.
		{"a report with no cost is unmeasured, not zero", `{"is_error":false,"num_turns":1}`, false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRunReport([]byte(tt.in))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.TotalCostUSD != tt.wantUSD {
				t.Errorf("usd = %v, want %v", got.TotalCostUSD, tt.wantUSD)
			}
			if got.tokens() != tt.wantTok {
				t.Errorf("tokens = %d, want %d — cache reads are cheaper but not free", got.tokens(), tt.wantTok)
			}
		})
	}
}

// Cache tokens dominate an agent run. Omitting them understates the bill by an
// order of magnitude, which would make the cost test decide the wrong way.
func TestTokensIncludeCache(t *testing.T) {
	var r runReport
	r.Usage.InputTokens, r.Usage.OutputTokens = 10, 20
	r.Usage.CacheReadInput, r.Usage.CacheCreationInput = 5000, 300
	if got := r.tokens(); got != 5330 {
		t.Errorf("tokens = %d, want 5330", got)
	}
}

func TestWithAgentReplacesRatherThanAppends(t *testing.T) {
	got := withAgent([]string{"PATH=/bin", "FLYWHEEL_AGENT=inherited/wrong", "HOME=/h"}, "fleet/builder-go")
	var agents []string
	for _, kv := range got {
		if strings.HasPrefix(kv, "FLYWHEEL_AGENT=") {
			agents = append(agents, kv)
		}
	}
	if len(agents) != 1 || agents[0] != "FLYWHEEL_AGENT=fleet/builder-go" {
		t.Errorf("FLYWHEEL_AGENT entries = %v, want exactly the assigned one", agents)
	}
	if len(got) != 3 {
		t.Errorf("env has %d entries, want 3 — the inherited one is replaced, not added to", len(got))
	}
}

// End-to-end against the real guard.sh, because the bug this replaces was
// invisible to every unit test: logSpend passed agent= as a k=v pair, guard.sh
// refuses that reserved key with exit 2 and writes nothing, and the error was
// discarded. Everything looked green while the ledger stayed empty.
func TestLogSpendReachesTheLedger(t *testing.T) {
	repo := t.TempDir()
	// inDir, not a bare exec.Command: git exports GIT_DIR to its hooks, so under
	// the pre-commit gate this init ran against the real repository, and under
	// CI's hook-like env it fails outright with "/nonexistent: Permission
	// denied". Same class as fw-c1l, one file over.
	if out, err := inDir(repo, "git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tools/flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../flywheel/guard.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools/flywheel/guard.sh"), src, 0o755); err != nil {
		t.Fatal(err)
	}

	// The environment carries the wrong agent; attribution must come from the
	// assignment regardless (ADR 0003).
	t.Setenv("FLYWHEEL_AGENT", "inherited/wrong")

	a := Assignment{Repo: "r", Bead: "fw-x.1", Agent: "fleet/builder-go"}
	var rep runReport
	rep.TotalCostUSD = 0.0772740
	rep.NumTurns = 7
	rep.Usage.InputTokens, rep.Usage.OutputTokens = 2, 4
	rep.Usage.CacheReadInput, rep.Usage.CacheCreationInput = 16016, 6822

	if err := logSpend(repo, a, rep); err != nil {
		t.Fatalf("logSpend: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(repo, ".flywheel", "agent-log.jsonl"))
	if err != nil {
		t.Fatalf("nothing reached the ledger: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
	}
	var rec map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unparseable record %q: %v", lines[0], err)
	}
	want := map[string]string{
		"event":  "bead.cost",
		"agent":  "fleet/builder-go",
		"bead":   "fw-x.1",
		"usd":    "0.0773",
		"tokens": "22844",
		"turns":  "7",
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("%s = %q, want %q (record: %s)", k, rec[k], v, lines[0])
		}
	}
}

// The refusal that started this: a reserved key must never appear in the argv.
func TestLogSpendNeverPassesAReservedKey(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "tools/flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stub that records its own argv instead of writing a log.
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + filepath.Join(repo, "argv.txt") + "\n"
	if err := os.WriteFile(filepath.Join(repo, "tools/flywheel/guard.sh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := logSpend(repo, Assignment{Bead: "fw-x.1", Agent: "fleet/builder-go"}, runReport{}); err != nil {
		t.Fatal(err)
	}
	argv, err := os.ReadFile(filepath.Join(repo, "argv.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range strings.Split(strings.TrimSpace(string(argv)), "\n") {
		if k, _, ok := strings.Cut(arg, "="); ok {
			for _, reserved := range []string{"ts", "event", "agent", "repo"} {
				if k == reserved {
					t.Errorf("argv carries reserved key %q — guard.sh refuses it and writes nothing", arg)
				}
			}
		}
	}
}
