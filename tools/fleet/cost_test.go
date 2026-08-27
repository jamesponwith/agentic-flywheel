package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func logRepo(t *testing.T, lines ...string) Repo {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".flywheel", "agent-log.jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Repo{Name: "r", Path: dir}
}

func TestReadSpendDistinguishesZeroFromUnmeasured(t *testing.T) {
	// Zero spend and unmeasured spend look identical in a total and mean
	// opposite things. A made-up number on a public dashboard is worse than
	// an absent one.
	repo := logRepo(t,
		`{"ts":"2026-08-16T10:00:00Z","event":"bead.claimed"}`,
		`{"ts":"2026-08-16T10:30:00Z","event":"bead.pr_opened"}`)
	sp, err := ReadSpend(repo, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Measured {
		t.Error("claimed to have measured a cost that was never recorded")
	}
	if _, ok := sp.PerGreenPR(); ok {
		t.Error("produced a per-PR cost from no cost data")
	}
	if !strings.Contains(sp.String(), "unmeasured") {
		t.Errorf("rendering hides that nothing was measured: %q", sp.String())
	}
}

func TestReadSpendSumsRecordedCost(t *testing.T) {
	repo := logRepo(t,
		`{"ts":"2026-08-16T10:00:00Z","event":"bead.claimed","usd":"1.50","tokens":"1000"}`,
		`{"ts":"2026-08-16T11:00:00Z","event":"bead.pr_opened","usd":"2.50","tokens":"2000"}`)
	sp, _ := ReadSpend(repo, time.Time{})
	if !sp.Measured || sp.USD != 4.0 || sp.Tokens != 3000 {
		t.Fatalf("spend = %+v, want $4.00 / 3000 tokens", sp)
	}
	per, ok := sp.PerGreenPR()
	if !ok || per != 4.0 {
		t.Errorf("per green PR = %v (%v), want 4.0", per, ok)
	}
}

func TestReadSpendDeduplicatesWholeLines(t *testing.T) {
	// guard.sh restore-log merges the mirror into the repo copy with `sort -u`
	// over whole records, on the stated grounds that each line carries its own
	// ts and so two identical lines ARE the same event. This is the only reader
	// of that ledger and it has to agree. The first case is the live pair for
	// fw-dov, both stamped 2026-08-20T22:28:25Z.
	for _, tc := range []struct {
		name                    string
		lines                   []string
		builders, green, tokens int
		usd                     float64
	}{
		{
			name: "a byte-identical pair is one event",
			lines: []string{
				`{"ts":"2026-08-20T22:28:25Z","event":"bead.claimed","bead":"fw-dov"}`,
				`{"ts":"2026-08-20T22:28:25Z","event":"bead.claimed","bead":"fw-dov"}`,
				`{"ts":"2026-08-20T22:28:25Z","event":"bead.gate_green","bead":"fw-dov"}`,
				`{"ts":"2026-08-20T22:28:25Z","event":"bead.gate_green","bead":"fw-dov"}`,
			},
			builders: 1, green: 1,
		},
		{
			// The guard against over-collapsing: a bead claimed, abandoned and
			// claimed again, or opening two PRs, differs only in ts. A key of
			// (event, bead) would swallow the second of each, trading this
			// overcount for an undercount.
			name: "events differing only in ts are two events",
			lines: []string{
				`{"ts":"2026-08-16T10:00:00Z","event":"bead.claimed","bead":"fw-x"}`,
				`{"ts":"2026-08-16T14:00:00Z","event":"bead.claimed","bead":"fw-x"}`,
				`{"ts":"2026-08-16T15:00:00Z","event":"bead.pr_opened","bead":"fw-x"}`,
				`{"ts":"2026-08-16T16:00:00Z","event":"bead.pr_opened","bead":"fw-x"}`,
			},
			builders: 2, green: 2,
		},
		{
			// Deduplicating before the parse covers the numerator as well as
			// the denominator. Nothing emits a duplicated cost record today.
			name: "a duplicated cost is billed once",
			lines: []string{
				`{"ts":"2026-08-16T10:00:00Z","event":"bead.cost","bead":"fw-x","usd":"3.00","tokens":"900"}`,
				`{"ts":"2026-08-16T10:00:00Z","event":"bead.cost","bead":"fw-x","usd":"3.00","tokens":"900"}`,
			},
			usd: 3.0, tokens: 900,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp, err := ReadSpend(logRepo(t, tc.lines...), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if sp.Builders != tc.builders || sp.Green != tc.green || sp.USD != tc.usd || sp.Tokens != tc.tokens {
				t.Errorf("spend = %d builder(s), %d green, $%v, %d tokens; want %d, %d, $%v, %d",
					sp.Builders, sp.Green, sp.USD, sp.Tokens,
					tc.builders, tc.green, tc.usd, tc.tokens)
			}
		})
	}
}

func TestReadSpendSurvivesACorruptLine(t *testing.T) {
	// One bad line must not lose the ledger — the log is append-only from
	// concurrent builders, so a torn write is a question of when, not if.
	repo := logRepo(t,
		`{"ts":"2026-08-16T10:00:00Z","event":"bead.claimed","usd":"1.00"}`,
		`{"ts":"2026-08-16T10:0`,
		`{"ts":"2026-08-16T11:00:00Z","event":"bead.claimed","usd":"2.00"}`)
	sp, err := ReadSpend(repo, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if sp.USD != 3.0 {
		t.Errorf("USD = %v, want 3.0 — a corrupt line lost real entries", sp.USD)
	}
}

func TestReadSpendHonoursTheWindow(t *testing.T) {
	repo := logRepo(t,
		`{"ts":"2026-01-01T10:00:00Z","event":"bead.claimed","usd":"99.00"}`,
		`{"ts":"2026-08-16T10:00:00Z","event":"bead.claimed","usd":"1.00"}`)
	sp, _ := ReadSpend(repo, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if sp.USD != 1.0 {
		t.Errorf("USD = %v, want 1.0 — spend outside the window was counted", sp.USD)
	}
}

// The two OverBudget tests lived here. They went with the mechanism: nothing
// gates on dollars any more, and a test for a deleted gate is a test that
// passes forever without asserting anything about the system.
//
// The property worth keeping was "an unmeasured repo is neither within budget
// nor over it — silence is the honest answer". It survives as
// TestUnmeasuredIsNotHeadroom, which asserts it about the reading that
// replaced them.
