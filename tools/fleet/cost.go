// Cost accounting (fw-e7e.2, fw-oef.4).
//
// The number every engineering org will need within a year and almost nobody
// measures on their own work. It cannot come from the GitHub API — the API
// knows what shipped, not what it cost — so it comes from the audit log the
// builders already write.
//
// Deliberately conservative: a run with no recorded cost contributes nothing
// rather than an estimate. A made-up number on a public dashboard is worse
// than an absent one, and this dashboard is the whole argument for the project.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Spend is what one repo cost over a window.
type Spend struct {
	Repo     string  `json:"repo"`
	Runs     int     `json:"runs"`
	Builders int     `json:"builders"`
	Green    int     `json:"green"`
	USD      float64 `json:"usd"`
	Tokens   int     `json:"tokens"`
	// Measured is false when no run recorded a cost. The distinction matters:
	// zero spend and unmeasured spend look identical in a total and mean
	// opposite things.
	Measured bool `json:"measured"`
}

// PerGreenPR is the headline: what a merged agent PR actually costs. Returns
// false when nothing was measured, so a caller renders "n/a" rather than 0.
func (s Spend) PerGreenPR() (float64, bool) {
	if !s.Measured || s.Green == 0 {
		return 0, false
	}
	return s.USD / float64(s.Green), true
}

// ReadSpend sums the audit log for a repo. Entries carry usd= and tokens= when
// the runner reported them; entries that do not are counted as activity but
// not as cost, which is what keeps Measured honest.
func ReadSpend(repo Repo, since time.Time) (Spend, error) {
	sp := Spend{Repo: repo.Name}
	f, err := os.Open(filepath.Join(repo.Path, ".flywheel", "agent-log.jsonl"))
	if err != nil {
		return sp, nil // no log is not an error; it is an unrun fleet
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var e map[string]string
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue // a corrupt line must not lose the whole ledger
		}
		ts, err := time.Parse(time.RFC3339, e["ts"])
		if err != nil || ts.Before(since) {
			continue
		}
		switch e["event"] {
		case "bead.claimed":
			sp.Builders++
		case "bead.gate_green", "bead.pr_opened":
			sp.Green++
		}
		if v, ok := e["usd"]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				sp.USD += f
				sp.Measured = true
			}
		}
		if v, ok := e["tokens"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				sp.Tokens += n
				sp.Measured = true
			}
		}
	}
	return sp, sc.Err()
}

// OverBudgetRepos reports repos past their declared monthly allowance. A repo
// that has spent its share pauses until the next window rather than quietly
// continuing — that is what makes ADR 0001's cost test enforceable rather
// than decorative.
func OverBudgetRepos(r Roster, since time.Time) ([]Spend, error) {
	var over []Spend
	for _, repo := range r.Repos {
		if repo.BudgetUSDMonth <= 0 {
			continue
		}
		sp, err := ReadSpend(repo, since)
		if err != nil {
			return over, err
		}
		if sp.Measured && sp.USD > float64(repo.BudgetUSDMonth) {
			over = append(over, sp)
		}
	}
	sort.Slice(over, func(i, j int) bool { return over[i].USD > over[j].USD })
	return over, nil
}

func (s Spend) String() string {
	if !s.Measured {
		return fmt.Sprintf("  %-22s %d builder(s), %d green — cost unmeasured", s.Repo, s.Builders, s.Green)
	}
	per := "n/a"
	if v, ok := s.PerGreenPR(); ok {
		per = fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("  %-22s %d builder(s), %d green, $%.2f total, %s per green PR",
		s.Repo, s.Builders, s.Green, s.USD, per)
}
