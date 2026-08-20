package main

import "testing"

func TestParseRunReport(t *testing.T) {
	real := `Some prose the agent wrote first.
{"is_error":false,"num_turns":7,"total_cost_usd":0.0772740,"usage":{"input_tokens":2,"cache_creation_input_tokens":6822,"cache_read_input_tokens":16016,"output_tokens":4}}`

	tests := []struct {
		name    string
		in      string
		wantOK  bool
		wantUSD float64
		wantTok int
	}{
		{"a real runner report after prose", real, true, 0.077274, 22844},
		{"no JSON at all leaves the run unmeasured", "I stopped at step 4.", false, 0, 0},
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
