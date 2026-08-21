// Recording what a builder actually cost (retro 2026-08, item 2).
//
// `fleet cost` shipped with the right distinction — zero spend and UNMEASURED
// spend mean opposite things — and then measured nothing: 0 `usd` entries in 22
// log lines. It is the instrument ADR 0001's cost test depends on, so an
// unmeasured instrument makes the test undecidable, which is how four
// mechanisms got built with nothing able to say no.
//
// The runner already knows. `claude -p --output-format json` returns
// total_cost_usd and a usage block; the cost is read from the runner's own
// report rather than estimated, because an estimate on a public dashboard is
// the thing cost.go was written to refuse.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// runReport is the subset of an agent runner's JSON result we record.
type runReport struct {
	TotalCostUSD float64 `json:"total_cost_usd"`
	NumTurns     int     `json:"num_turns"`
	IsError      bool    `json:"is_error"`
	Usage        struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		CacheReadInput     int `json:"cache_read_input_tokens"`
		CacheCreationInput int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	// ModelUsage is the per-model breakdown, and on a run that ended in an API
	// error it is the only place the totals survive: `usage` describes the last
	// request, which for a 429 is the one that bought nothing. The first real
	// night reported usage all-zero next to 19,650,183 cache-read tokens here.
	ModelUsage map[string]struct {
		InputTokens        int `json:"inputTokens"`
		OutputTokens       int `json:"outputTokens"`
		CacheReadInput     int `json:"cacheReadInputTokens"`
		CacheCreationInput int `json:"cacheCreationInputTokens"`
	} `json:"modelUsage"`
	// APIErrorStatus is the HTTP status that ended the run. 429 means the run
	// stopped because the account ran out of quota, not because the work was
	// hard — the fleet must stop dispatching rather than spend the rest of the
	// night discovering the same wall once per builder.
	APIErrorStatus int    `json:"api_error_status"`
	Result         string `json:"result"`
}

// rateLimited reports whether the run died on quota rather than on the work.
func (r runReport) rateLimited() bool { return r.APIErrorStatus == 429 }

// tokens is every token the run consumed, cache included. Cache reads are
// cheaper but not free, and omitting them would understate the bill.
func (r runReport) tokens() int {
	n := r.Usage.InputTokens + r.Usage.OutputTokens +
		r.Usage.CacheReadInput + r.Usage.CacheCreationInput
	if n > 0 {
		return n
	}
	// Fall back to the per-model breakdown, which outlives an API error. Only
	// when `usage` totals zero: when both are populated they describe the same
	// tokens, and adding them would double the bill.
	for _, m := range r.ModelUsage {
		n += m.InputTokens + m.OutputTokens + m.CacheReadInput + m.CacheCreationInput
	}
	return n
}

// parseRunReport pulls the runner's report out of its output: the last complete
// JSON object in the stream that carries a cost. Anything before it is the
// agent's prose.
//
// Decoding is what identifies the report — never where its keys happen to sit.
// An earlier version anchored on the literal `{"is_error"`, which silently
// required is_error to be the object's FIRST key: the same report with `type`
// first, or pretty-printed, parsed as nothing and left every run unmeasured.
// No producer orders the keys of a JSON object, and runner.go exists precisely
// so the runner can be swapped for one that reports differently.
//
// It returns ok=false rather than a zero report when there is nothing to parse —
// a runner that does not report cost leaves the run UNMEASURED, which cost.go
// already renders honestly. Guessing here would defeat the whole distinction.
func parseRunReport(out []byte) (runReport, bool) {
	// Backwards, so the *last* report wins and the scan stops as soon as it
	// finds one. Nested objects sit at higher offsets than the object that
	// contains them, so they are tried and rejected first for carrying no cost.
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] != '{' {
			continue
		}
		var r runReport
		// Decode, not Unmarshal: Decode reads one value and ignores whatever
		// trails it, so a report followed by a stray brace still parses.
		if err := json.NewDecoder(bytes.NewReader(out[i:])).Decode(&r); err != nil {
			continue
		}
		if r.TotalCostUSD == 0 && r.tokens() == 0 {
			continue // parsed something, but it carried no cost
		}
		return r, true
	}
	return runReport{}, false
}

// withAgent replaces FLYWHEEL_AGENT in env, so attribution comes from the
// assignment and never from whatever the environment happened to carry.
func withAgent(env []string, name string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "FLYWHEEL_AGENT=") {
			out = append(out, kv)
		}
	}
	return append(out, "FLYWHEEL_AGENT="+name)
}

// logSpend appends the run's cost to the repo's audit log through guard.sh, so
// it lands in the same append-only ledger `fleet cost` already reads.
//
// The agent goes in the environment, never in a k=v pair. guard.sh writes
// "agent" for itself and refuses a caller-supplied duplicate outright — exit 2,
// nothing written, not even a partial record — so passing `agent=` here
// recorded no cost at all while looking like it worked.
//
// Scrubbed before it is set, for the reason build() gives: hermeticEnv
// preserves an inherited FLYWHEEL_AGENT, and a cost line attributed to the
// coordinator's shell rather than to the assignment is exactly the attribution
// bug the ADR 0003 log exists to prevent.
func logSpend(repoPath string, a Assignment, r runReport) error {
	cmd := inDir(repoPath, "tools/flywheel/guard.sh", "log", "bead.cost",
		"bead="+a.Bead,
		"usd="+strconv.FormatFloat(r.TotalCostUSD, 'f', 4, 64),
		"tokens="+strconv.Itoa(r.tokens()),
		"turns="+strconv.Itoa(r.NumTurns),
	)
	cmd.Env = withAgent(cmd.Env, a.Agent)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("recording spend for %s: %w: %s", a.Bead, err, strings.TrimSpace(string(out)))
	}
	return nil
}
