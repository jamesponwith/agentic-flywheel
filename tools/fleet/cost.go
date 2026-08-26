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

// greenPR identifies the PR a green event is about. One PR is one green no
// matter how many events announce it.
type greenPR struct{ bead, pr string }

// ReadSpend sums the audit log for a repo. Entries carry usd= and tokens= when
// the runner reported them; entries that do not are counted as activity but
// not as cost, which is what keeps Measured honest.
func ReadSpend(repo Repo, since time.Time) (Spend, error) {
	sp := Spend{Repo: repo.Name}
	counted := map[greenPR]bool{}
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
			// One PR is one green, however many events announce it. fw-oef.8
			// logged bead.pr_opened (pr=1) and then bead.gate_green (pr=1) six
			// minutes later; both reached here and one PR doubled the
			// denominator under a measured numerator (fw-d20).
			//
			// Keyed on (bead, pr) and not on bead alone: a bead may
			// legitimately open more than one PR, and collapsing per bead
			// would trade this overcount for an undercount.
			//
			// A green missing either field cannot be correlated with a PR, so
			// it still counts per event. That is the older behaviour and NOT the
			// conservative direction — a larger denominator makes the fleet
			// look cheaper per PR, not dearer. It is kept because the events
			// that hit it are exact-duplicate ledger records, which want
			// whole-line deduplication rather than this key (fw-6gc).
			//
			// NOTE: no code in this repo emits bead.gate_green — every
			// `guard.sh log` call site writes bead.claimed, bead.pr_opened,
			// bead.abandoned or gate.skipped. The event is written by hand or
			// by agents following the skill, so what it carries is convention,
			// not a guarantee. Only the fw-oef.8 pair has ever named a pr.
			k := greenPR{e["bead"], e["pr"]}
			if k.bead == "" || k.pr == "" || !counted[k] {
				// The unkeyed case records a key it will never consult: the
				// emptiness tests short-circuit ahead of the map on every
				// later visit, so such a green always counts.
				counted[k] = true
				sp.Green++
			}
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

// FleetWindowSpend is what every repo together consumed since `since`, valued
// in the runner's units.
//
// Fleet-wide and not per-repo, because the thing being rationed is one account.
// Two builders in different repos drain the same pool, and a per-repo view
// cannot see that: on the first unattended night each of them was individually
// under every limit the roster expressed, and together they took the account
// away from its owner.
//
// ok is false when nothing in the window recorded a cost — unmeasured, which
// is not the same as free, and which must not be read as headroom.
func FleetWindowSpend(r Roster, since time.Time) (usd float64, ok bool, err error) {
	for _, repo := range r.Repos {
		sp, e := ReadSpend(repo, since)
		if e != nil {
			// One unreadable repo must not silently shrink the total, which
			// would read as headroom the account does not have.
			return 0, false, e
		}
		if sp.Measured {
			usd += sp.USD
			ok = true
		}
	}
	return usd, ok, nil
}
