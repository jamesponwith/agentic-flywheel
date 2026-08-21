// The account's limit is a period, not a bill.
//
// The first gate capped dollars per repo per month. Both halves were wrong for
// a subscription: it charges no marginal dollar, so total_cost_usd is
// API-equivalent valuation rather than money, and the limit that actually
// stopped the fleet was an account-wide window that resets in hours. The gate
// therefore paused one repo for a month over a quota that had already reset,
// and could not see two builders draining one shared pool between them.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ledger writes an audit log for a repo whose entries are `age` old.
func ledger(t *testing.T, name string, age time.Duration, usd ...float64) Repo {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	var body string
	for _, u := range usd {
		body += fmt.Sprintf(`{"ts":%q,"event":"bead.cost","repo":%q,"usd":"%.4f"}`+"\n",
			time.Now().Add(-age).UTC().Format(time.RFC3339), name, u)
	}
	if err := os.WriteFile(filepath.Join(dir, ".flywheel", "agent-log.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Repo{Name: name, Path: dir}
}

func TestWindowSpendIsFleetWideBecauseTheQuotaIs(t *testing.T) {
	// The failure this exists for: on the first night each builder was under
	// every per-repo limit the roster expressed, and together they emptied the
	// account. A per-repo view cannot see that; only the sum can.
	r := Roster{Repos: []Repo{
		ledger(t, "a", time.Hour, 14.64),
		ledger(t, "b", time.Hour, 16.24),
	}}
	usd, ok, err := FleetWindowSpend(r, time.Now().Add(-5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("reported unmeasured despite two ledgers carrying costs")
	}
	if fmt.Sprintf("%.2f", usd) != "30.88" {
		t.Errorf("fleet spend = %.2f, want 30.88 — the sum, not the larger repo", usd)
	}
}

func TestTheWindowResetsAndTheFleetIsReleased(t *testing.T) {
	// The whole point of the change. Under the monthly model this spend still
	// counts and the repo stays paused; under the window model the quota came
	// back hours ago and the fleet may run.
	r := Roster{Repos: []Repo{ledger(t, "a", 19*time.Hour, 30.87)}}

	usd, ok, err := FleetWindowSpend(r, time.Now().Add(-5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ok || usd != 0 {
		t.Errorf("spend from 19h ago still counted against a 5h window: %.2f — "+
			"that is the month-long pause the reset was supposed to end", usd)
	}
	// And it is still visible over a window long enough to contain it, so the
	// release is the window doing its job rather than the ledger losing data.
	if _, ok, _ := FleetWindowSpend(r, time.Now().Add(-24*time.Hour)); !ok {
		t.Error("the spend vanished entirely; the window is not filtering, it is dropping")
	}
}

func TestUnmeasuredIsNotHeadroom(t *testing.T) {
	// A fleet that has recorded nothing has not proven it spent nothing, and
	// $0.00 read as headroom is how an unmeasured instrument authorises a run.
	r := Roster{Repos: []Repo{ledger(t, "a", time.Hour)}}
	usd, ok, err := FleetWindowSpend(r, time.Now().Add(-5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("claimed a measurement from a ledger with no costs in it")
	}
	if usd != 0 {
		t.Errorf("invented %.2f from nothing", usd)
	}
}

func TestQuotaWindowDefaultsToTheSessionWindow(t *testing.T) {
	if got := (Quota{}).window(); got != 5*time.Hour {
		t.Errorf("default window = %s, want 5h — a roster that omits it must not "+
			"default to something that never expires", got)
	}
	if got := (Quota{WindowHours: 168}).window(); got != 168*time.Hour {
		t.Errorf("window = %s, want 168h; a weekly cap must be expressible", got)
	}
}

func TestCommittedRosterRationsTheWindowNotTheMonth(t *testing.T) {
	r, err := LoadRoster("../../.flywheel/roster.json")
	if err != nil {
		t.Skip("roster not present")
	}
	q := r.Caps.Quota
	if q.BudgetUSDWindow <= 0 {
		t.Fatal("roster declares no window budget, so nothing gates dispatch and " +
			"the only backstop left is discovering the 429 one builder at a time")
	}
	if q.window() > 24*time.Hour {
		t.Errorf("window is %s: long enough that one bad night pauses the fleet for "+
			"days over a quota that reset the same evening", q.window())
	}
}
