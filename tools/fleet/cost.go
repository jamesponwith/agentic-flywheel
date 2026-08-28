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
	Repo     string `json:"repo"`
	Runs     int    `json:"runs"`
	Builders int    `json:"builders"`
	Green    int    `json:"green"`
	// GreenMeasured is the subset of Green whose bead also logged a cost. The
	// per-green ratio is only honest when it equals Green; otherwise measured
	// spend is being divided by unmeasured activity and the figure is
	// understated by construction (fw-ax2).
	GreenMeasured int     `json:"green_measured"`
	USD           float64 `json:"usd"`
	Tokens        int     `json:"tokens"`
	// Measured is false when no run recorded a cost. The distinction matters:
	// zero spend and unmeasured spend look identical in a total and mean
	// opposite things.
	Measured bool `json:"measured"`
}

// PerGreenPR is the headline: what a merged agent PR actually costs. Returns
// false when nothing was measured, or when any green in the window came from
// a run that recorded no cost, so a caller renders "n/a" rather than a number
// that is too small by construction.
func (s Spend) PerGreenPR() (float64, bool) {
	if !s.Measured || s.Green == 0 || s.GreenMeasured != s.Green {
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
	// ponytail: the whole set of distinct lines is held in memory. At the
	// ledger's size — hundreds of records — that is nothing; if it ever
	// matters, key on a hash of the line instead of the line.
	seen := map[string]bool{}
	counted := map[greenPR]bool{}
	// Correlation for GreenMeasured, keyed by the bead field guard.sh already
	// writes. An empty id never joins costBeads, so a green with no bead=
	// cannot match anything and counts as unmeasured (fail closed).
	//
	// ponytail: the key is the bead, not the run. A bead that logged a cost
	// once vouches for every green it ever produces, including a second PR
	// from a later run that recorded nothing. Cost lines carry no pr= today;
	// when they do, key on (bead, pr) like `counted` and the ceiling goes.
	costBeads := map[string]bool{}
	greenBeads := map[string]int{}
	f, err := os.Open(filepath.Join(repo.Path, ".flywheel", "agent-log.jsonl"))
	if err != nil {
		return sp, nil // no log is not an error; it is an unrun fleet
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		// A byte-identical repeat is the same event, not a second one. That is
		// guard.sh restore-log's rule, stated where it merges the mirror with
		// `sort -u`: each line carries its own ts, so two identical lines ARE
		// the same event. .gitattributes marks this ledger merge=union and
		// git's union driver concatenates without deduplicating, so branches
		// keep producing them — the live log holds a duplicated bead.claimed
		// and a duplicated bead.gate_green for fw-dov, both stamped
		// 2026-08-20T22:28:25Z, counted as an extra builder and an extra green
		// for a single run (fw-6gc).
		//
		// Whole line, not (event, bead): a bead legitimately claimed twice, or
		// opening two PRs, differs only in ts, and collapsing on the event
		// would trade this overcount for an undercount. Deduplicating before
		// the parse covers usd= too, so fw-d20's green deduplication cannot
		// turn a duplicated cost-carrying green into an inflated numerator.
		//
		// This closes the duplicate-line route and only that one. A single PR
		// that logs both bead.pr_opened and bead.gate_green still counts as two
		// greens; that is fw-d20 — different key, separate change.
		line := sc.Text()
		if seen[line] {
			continue
		}
		seen[line] = true

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
				greenBeads[k.bead]++
			}
		}
		measured := false
		if v, ok := e["usd"]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				sp.USD += f
				measured = true
			}
		}
		if v, ok := e["tokens"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				sp.Tokens += n
				measured = true
			}
		}
		if measured {
			sp.Measured = true
			if e["bead"] != "" {
				costBeads[e["bead"]] = true
			}
		}
	}
	for bead := range costBeads {
		sp.GreenMeasured += greenBeads[bead]
	}
	return sp, sc.Err()
}

func (s Spend) String() string {
	if !s.Measured {
		return fmt.Sprintf("  %-22s %d builder(s), %d green — cost unmeasured", s.Repo, s.Builders, s.Green)
	}
	head := fmt.Sprintf("  %-22s %d builder(s), %d green, $%.2f total, ", s.Repo, s.Builders, s.Green, s.USD)
	if v, ok := s.PerGreenPR(); ok {
		return head + fmt.Sprintf("$%.2f per green PR", v)
	}
	if s.Green == 0 {
		return head + "per green PR n/a (no green PRs)"
	}
	// Name the shortfall when refusing: the greens exist, the costs exist,
	// and they are not the same runs.
	return head + fmt.Sprintf("per green PR n/a (%d of %d greens came from a run that recorded a cost)",
		s.GreenMeasured, s.Green)
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
