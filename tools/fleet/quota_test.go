// What the first real unattended night cost, and what it taught.
//
// Two builders ran concurrently against one account, both died on HTTP 429
// ("You've hit your session limit"), and the run reported them as abandoned
// with nothing to show. All three claims in that sentence were wrong in a way
// worth a test: the money was real ($30.87 against a $15/month budget), the
// work was real (five commits and a pull request), and the second builder's
// $16 bought a second copy of the same error message.
package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

// realReport is the shape the runner actually emitted that night, reduced to
// the fields that mattered. Kept as JSON rather than a struct literal so it
// tests the parser and not just the arithmetic.
const realReport = `{"type":"result","subtype":"success","is_error":true,
"total_cost_usd":14.635341500000001,"num_turns":1,
"usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},
"modelUsage":{"claude-opus-5[1m]":{"inputTokens":276,"outputTokens":106754,
"cacheReadInputTokens":19650183,"cacheCreationInputTokens":214002,"costUSD":14.6353415}},
"api_error_status":429,"terminal_reason":"api_error",
"result":"You've hit your session limit · resets 6pm (America/Los_Angeles)"}`

func TestTokensSurviveAnAPIError(t *testing.T) {
	// `usage` describes the LAST request, which for a 429 is the one that
	// bought nothing — it came back all zeros next to 19.6M cache-read tokens
	// in modelUsage. Reading only `usage` reported a $14.64 run as 0 tokens,
	// which is the "unmeasured reads as free" failure one level down.
	rep, ok := parseRunReport([]byte(realReport))
	if !ok {
		t.Fatal("did not parse the report the runner actually emitted")
	}
	if rep.TotalCostUSD == 0 {
		t.Error("lost the cost")
	}
	want := 276 + 106754 + 19650183 + 214002
	if got := rep.tokens(); got != want {
		t.Errorf("tokens = %d, want %d — cache reads dominate an agent run by "+
			"three orders of magnitude, so dropping them is not a rounding error", got, want)
	}
}

func TestTokensAreNotDoubleCounted(t *testing.T) {
	// When both blocks are populated they describe the same tokens. Summing
	// them would overstate the bill and push a repo over budget on arithmetic.
	in := `{"total_cost_usd":1,"usage":{"input_tokens":10,"output_tokens":20,
	"cache_read_input_tokens":30,"cache_creation_input_tokens":40},
	"modelUsage":{"m":{"inputTokens":10,"outputTokens":20,
	"cacheReadInputTokens":30,"cacheCreationInputTokens":40}}}`
	rep, ok := parseRunReport([]byte(in))
	if !ok {
		t.Fatal("did not parse")
	}
	if got := rep.tokens(); got != 100 {
		t.Errorf("tokens = %d, want 100 (counted once, not twice)", got)
	}
}

func TestRateLimitIsNotTheBeadsFault(t *testing.T) {
	rep, ok := parseRunReport([]byte(realReport))
	if !ok {
		t.Fatal("did not parse")
	}
	if !rep.rateLimited() {
		t.Error("429 not recognised as a quota wall; the fleet would dispatch " +
			"every remaining builder into it, one startup cost at a time")
	}
	// A non-429 failure must NOT look like a quota wall, or a genuinely broken
	// bead would silently halt the whole night.
	var other runReport
	if err := json.Unmarshal([]byte(`{"total_cost_usd":1,"api_error_status":500}`), &other); err != nil {
		t.Fatal(err)
	}
	if other.rateLimited() {
		t.Error("500 treated as a quota wall")
	}
}

func TestSalvageKeepsTheWorkANonZeroExitWouldHaveHidden(t *testing.T) {
	// The classification checked runErr before it counted commits, so a builder
	// that made five commits and opened PR #45 before dying on the 429 reported
	// as "abandoned" — and the night read as $30.87 for nothing.
	cases := []struct {
		name    string
		runErr  error
		commits int
		want    string
	}{
		{"died with work behind it", fmt.Errorf("exit status 1"), 5, "salvage"},
		{"died with nothing behind it", fmt.Errorf("exit status 1"), 0, "abandoned"},
		{"clean exit, no commits, still not green", nil, 0, "no-op"},
		{"clean exit with commits", nil, 3, "green"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.runErr, tc.commits, false)
			if got != tc.want {
				t.Errorf("outcome = %q, want %q", got, tc.want)
			}
		})
	}
}
