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
	return Repo{Name: "r", Path: dir, BudgetUSDMonth: 10}
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

func TestOverBudgetIgnoresUnmeasuredRepos(t *testing.T) {
	// A repo whose cost was never recorded must not be reported as within
	// budget OR over it. Silence is the honest answer.
	r := Roster{Repos: []Repo{logRepo(t, `{"ts":"2026-08-16T10:00:00Z","event":"bead.claimed"}`)}}
	over, err := OverBudgetRepos(r, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(over) != 0 {
		t.Errorf("reported an unmeasured repo as over budget: %+v", over)
	}
}

func TestOverBudgetCatchesRealOverspend(t *testing.T) {
	r := Roster{Repos: []Repo{logRepo(t,
		`{"ts":"2026-08-16T10:00:00Z","event":"bead.claimed","usd":"12.00"}`)}}
	over, _ := OverBudgetRepos(r, time.Time{})
	if len(over) != 1 || over[0].USD != 12.0 {
		t.Errorf("over = %+v, want the $12 repo against a $10 budget", over)
	}
}
