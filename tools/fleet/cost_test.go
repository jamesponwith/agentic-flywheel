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

func TestReadSpendCountsOnePROnce(t *testing.T) {
	// The real shape from .flywheel/agent-log.jsonl: fw-oef.8 logged
	// bead.pr_opened (pr=1) and then bead.gate_green (pr=1) six minutes later.
	// Both are green events about one PR; counting both halves the headline
	// per-green-PR cost independently of any measurement problem.
	//
	// Both events carry a cost here to pin the other half of the fix at the
	// same time: deduplication is about the denominator only. Each event
	// carries its own usd, so dropping the second green's cost would move the
	// numerator to repair the denominator and leave the headline just as wrong.
	repo := logRepo(t,
		`{"ts":"2026-08-15T21:52:15Z","event":"bead.pr_opened","bead":"fw-oef.8","pr":"1","usd":"3.00","tokens":"1000"}`,
		`{"ts":"2026-08-15T21:58:03Z","event":"bead.gate_green","bead":"fw-oef.8","pr":"1","usd":"1.50","tokens":"500"}`)
	sp, err := ReadSpend(repo, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Green != 1 {
		t.Errorf("green = %d, want 1 — one PR announced twice counted twice", sp.Green)
	}
	if sp.USD != 4.5 || sp.Tokens != 1500 {
		t.Errorf("spend = $%v / %d tokens, want $4.50 / 1500 — deduplication ate real cost", sp.USD, sp.Tokens)
	}
	if per, ok := sp.PerGreenPR(); !ok || per != 4.5 {
		t.Errorf("per green PR = %v (%v), want 4.5", per, ok)
	}
}

func TestReadSpendCountsTwoPRsFromOneBeadTwice(t *testing.T) {
	// The guard against fixing the overcount into an undercount: the rule is
	// one green per PR, not one green per bead. A bead that opens a second PR
	// after review has produced two green PRs and must read as two.
	repo := logRepo(t,
		`{"ts":"2026-08-15T21:52:15Z","event":"bead.pr_opened","bead":"fw-oef.8","pr":"1"}`,
		`{"ts":"2026-08-15T21:58:03Z","event":"bead.gate_green","bead":"fw-oef.8","pr":"1"}`,
		`{"ts":"2026-08-16T09:10:00Z","event":"bead.pr_opened","bead":"fw-oef.8","pr":"7"}`,
		`{"ts":"2026-08-16T09:20:00Z","event":"bead.gate_green","bead":"fw-oef.8","pr":"7"}`)
	sp, _ := ReadSpend(repo, time.Time{})
	if sp.Green != 2 {
		t.Errorf("green = %d, want 2 — deduplicated per bead instead of per PR", sp.Green)
	}
}

func TestReadSpendDoesNotDeduplicateGreensWithoutAPR(t *testing.T) {
	// A green naming no bead or no pr cannot be correlated with a PR, so it
	// keeps the per-event count. This asserts the SCOPE of the fix, not the
	// fix.
	//
	// It wanted two greens, and said so: the first two lines are the real
	// exact-duplicate pair from .flywheel/agent-log.jsonl, one event counted
	// twice, which a larger denominator turns into a fleet that looks cheaper
	// per PR. fw-6gc has since landed and deduplicates whole lines, so the
	// pair collapses to one and the answer is 2.
	//
	// What is still being asserted is the boundary between the two fixes: the
	// third line is a green with a pr and no bead, and it must NOT be folded
	// into the first by this key. Line deduplication handles identical
	// records; this key handles one PR announced twice. Neither reaches into
	// the other.
	repo := logRepo(t,
		`{"ts":"2026-08-20T22:28:25Z","event":"bead.gate_green","bead":"fw-dov"}`,
		`{"ts":"2026-08-20T22:28:25Z","event":"bead.gate_green","bead":"fw-dov"}`,
		`{"ts":"2026-08-20T22:30:00Z","event":"bead.pr_opened","pr":"58"}`)
	sp, _ := ReadSpend(repo, time.Time{})
	if sp.Green != 2 {
		t.Errorf("green = %d, want 2 — the duplicate pair is one event (fw-6gc), and the "+
			"unkeyed green beside it must still count on its own", sp.Green)
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
