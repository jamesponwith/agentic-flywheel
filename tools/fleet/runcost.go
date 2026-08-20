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
	"encoding/json"
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
}

// tokens is every token the run consumed, cache included. Cache reads are
// cheaper but not free, and omitting them would understate the bill.
func (r runReport) tokens() int {
	return r.Usage.InputTokens + r.Usage.OutputTokens +
		r.Usage.CacheReadInput + r.Usage.CacheCreationInput
}

// parseRunReport pulls the runner's report out of its output. It returns ok=false
// rather than a zero report when there is nothing to parse — a runner that does
// not report cost leaves the run UNMEASURED, which cost.go already renders
// honestly. Guessing here would defeat the whole distinction.
func parseRunReport(out []byte) (runReport, bool) {
	s := strings.TrimSpace(string(out))
	// The report is the last JSON object in the stream; anything before it is
	// the agent's prose.
	i := strings.LastIndex(s, "{\"is_error\"")
	if i < 0 {
		i = strings.LastIndex(s, "{")
	}
	if i < 0 {
		return runReport{}, false
	}
	var r runReport
	if err := json.Unmarshal([]byte(s[i:]), &r); err != nil {
		return runReport{}, false
	}
	if r.TotalCostUSD == 0 && r.tokens() == 0 {
		return runReport{}, false // parsed something, but it carried no cost
	}
	return r, true
}

// logSpend appends the run's cost to the repo's audit log through guard.sh, so
// it lands in the same append-only ledger `fleet cost` already reads.
func logSpend(repoPath string, a Assignment, r runReport) error {
	return inDir(repoPath, "tools/flywheel/guard.sh", "log", "bead.cost",
		"bead="+a.Bead,
		"agent="+a.Agent,
		"usd="+strconv.FormatFloat(r.TotalCostUSD, 'f', 4, 64),
		"tokens="+strconv.Itoa(r.tokens()),
		"turns="+strconv.Itoa(r.NumTurns),
	).Run()
}
