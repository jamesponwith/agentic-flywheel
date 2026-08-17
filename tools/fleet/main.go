// fleet: the cross-repo agent coordinator (ADR 0005 — fleet tooling lives here,
// never cloned into a project).
//
//	fleet claim <bead> -agent NAME [-ttl 45m]   take a bead under lease
//	fleet heartbeat <bead> -agent NAME          extend your own lease
//	fleet release <bead> -agent NAME            give it back
//	fleet reclaim                               sweep expired leases to ready
//	fleet status                                who holds what, across the fleet
//	fleet allocate                              tonight's plan (prints, never spawns)
//	fleet hydrate                               make every repo's beads readable
//	fleet bypasses                              gates that got skipped, and by how much
//	fleet adr-drift                             decisions this branch made without recording
//	fleet doctor                                which stages each repo is missing
//	fleet run                                   allocate, then spawn builders (needs -execute)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	agent := fs.String("agent", os.Getenv("FLYWHEEL_AGENT"), "agent name (or $FLYWHEEL_AGENT)")
	ttl := fs.Duration("ttl", defaultLeaseTTL, "lease duration")
	dir := fs.String("dir", ".", "repo directory")
	rosterPath := fs.String("roster", defaultRoster(), "path to roster.json")
	asJSON := fs.Bool("json", false, "JSON output")
	since := fs.String("since", "", "only consider history since this git date (e.g. 30.days)")
	base := fs.String("base", "origin/main", "base ref to compare this branch against (adr-drift)")
	source := fs.String("source", "", "template repo to copy missing artifacts from (doctor -fix)")
	fix := fs.Bool("fix", false, "install missing artifacts from -source; never overwrites")
	self := fs.Bool("self", false, "diagnose the current repo instead of a roster")
	onlyBead := fs.String("bead", "", "run exactly this bead, ignoring the queue (for a supervised run)")
	execute := fs.Bool("execute", false, "actually spawn builders (default is a dry run)")
	perBuilder := fs.Duration("per-builder", DefaultRunOpts().PerBuilder, "wall-clock cap per builder")

	// Subcommands taking a bead id read it before the flags.
	var bead string
	rest := os.Args[2:]
	switch cmd {
	case "claim", "heartbeat", "release":
		if len(rest) == 0 {
			fmt.Fprintf(os.Stderr, "%s: bead id required\n", cmd)
			os.Exit(2)
		}
		bead, rest = rest[0], rest[1:]
	}
	_ = fs.Parse(rest)

	l := leaser{bd: bdClient{dir: *dir, run: execBD}, now: time.Now}
	var err error
	switch cmd {
	case "claim":
		if err = l.Claim(bead, *agent, *ttl); err == nil {
			fmt.Printf("claimed %s for %s until %s\n", bead, *agent, time.Now().Add(*ttl).UTC().Format(time.RFC3339))
		}
	case "heartbeat":
		if err = l.Heartbeat(bead, *agent, *ttl); err == nil {
			fmt.Printf("extended %s to %s\n", bead, time.Now().Add(*ttl).UTC().Format(time.RFC3339))
		}
	case "release":
		if err = l.Release(bead, *agent); err == nil {
			fmt.Printf("released %s\n", bead)
		}
	case "reclaim":
		err = doReclaim(l, *asJSON)
	case "status":
		err = doStatus(*rosterPath, *asJSON)
	case "allocate":
		err = doAllocate(*rosterPath, *asJSON)
	case "hydrate":
		err = doHydrate(*rosterPath, *asJSON)
	case "bypasses":
		err = doBypasses(*rosterPath, *since, *asJSON)
	case "adr-drift":
		err = doADRDrift(*dir, *base, *asJSON)
	case "doctor":
		err = doDoctor(*rosterPath, *source, *fix, *asJSON, *self)
	case "run":
		err = doRun(*rosterPath, *execute, *perBuilder, *onlyBead, *asJSON)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func doReclaim(l leaser, asJSON bool) error {
	got, err := l.Reclaim()
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(got)
	}
	if len(got) == 0 {
		fmt.Println("no expired leases")
		return nil
	}
	for _, r := range got {
		fmt.Printf("reclaimed %s from %s (expired %s ago)\n", r.ID, r.Holder, r.Late.Round(time.Second))
	}
	return nil
}

func doStatus(rosterPath string, asJSON bool) error {
	r, err := LoadRoster(rosterPath)
	if err != nil {
		return err
	}
	type held struct {
		Repo, Bead, Title, Holder string
		Expires                   time.Time
	}
	var all []held
	for _, repo := range r.Repos {
		beads, err := (bdClient{dir: repo.Path, run: execBD}).list("in_progress")
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", repo.Name, err)
			continue
		}
		for _, b := range beads {
			holder, exp, ok := b.Lease()
			if !ok {
				holder = "(human — no lease)"
			}
			all = append(all, held{repo.Name, b.ID, b.Title, holder, exp})
		}
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(all)
	}
	if len(all) == 0 {
		fmt.Println("fleet idle — nothing in progress")
		return nil
	}
	now := time.Now()
	for _, h := range all {
		left := "—"
		if !h.Expires.IsZero() {
			d := h.Expires.Sub(now).Round(time.Second)
			left = d.String()
			if d < 0 {
				left = "EXPIRED " + (-d).String() + " ago"
			}
		}
		fmt.Printf("%-22s %-12s %-28s %s\n", h.Repo, h.Bead, h.Holder, left)
	}
	return nil
}

func doAllocate(rosterPath string, asJSON bool) error {
	r, err := LoadRoster(rosterPath)
	if err != nil {
		return err
	}
	plan, err := AllocateWithLoad(r, func(path string) bdClient {
		return bdClient{dir: path, run: execBD}
	}, ghReviewLoad, time.Now())
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}
	if plan.Stopped != "" {
		fmt.Printf("HALTED — kill switch set (%s)\nNo work allocated. Clear it with: tools/flywheel/guard.sh resume [--fleet]\n", plan.Stopped)
		return nil
	}
	fmt.Printf("plan for %s — review weight %d/%d (%d already in review), %d builders, %d repos\n\n",
		plan.GeneratedAt.Format("2006-01-02"), plan.Allocated+plan.InReview, plan.Budget,
		plan.InReview, r.Caps.ConcurrentBuilders, r.Caps.ReposPerNight)
	for _, a := range plan.Assignments {
		fmt.Printf("  %-22s %-12s w%d → %-24s %s\n", a.Repo, a.Bead, a.Weight, a.Agent, a.Title)
	}
	if len(plan.Assignments) == 0 {
		fmt.Println("  (nothing allocatable)")
	}
	if len(plan.Declined) > 0 {
		fmt.Println("\ndeclined:")
		for _, d := range plan.Declined {
			fmt.Printf("  %-22s %-12s %s\n", d.Repo, d.Bead, d.Reason)
		}
	}
	return nil
}

func doHydrate(rosterPath string, asJSON bool) error {
	r, err := LoadRoster(rosterPath)
	if err != nil {
		return err
	}
	// Resolve any gates whose PR has merged before diagnosing readiness — a
	// stale gate holds real work out of the queue (ADR 0014).
	if _, err := execBD(".", "gate", "check"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: gate check: %v\n", err)
	}
	res := Hydrate(r, liveHydrateOps())
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	for _, x := range res {
		line := fmt.Sprintf("  %-22s %s", x.Repo, x.Action)
		if x.Detail != "" {
			line += " — " + x.Detail
		}
		fmt.Println(line)
	}
	fmt.Println(summarize(res))
	// Non-zero so a scheduled run surfaces this instead of looking fine.
	var unreadable, mutated int
	for _, x := range res {
		switch x.Action {
		case "failed", "skipped":
			unreadable++
		case "mutated":
			mutated++
		}
	}
	switch {
	case mutated > 0 && unreadable > 0:
		return fmt.Errorf("%d repo(s) mutated by bd init, %d still unreadable", mutated, unreadable)
	case mutated > 0:
		return fmt.Errorf("%d repo(s) mutated by bd init — inspect before cutting a branch", mutated)
	case unreadable > 0:
		return fmt.Errorf("%d repo(s) still unreadable", unreadable)
	}
	return nil
}

func countBad(rs []HydrateResult) int {
	n := 0
	for _, r := range rs {
		if r.Action == "failed" || r.Action == "skipped" || r.Action == "mutated" {
			n++
		}
	}
	return n
}

func doBypasses(rosterPath, since string, asJSON bool) error {
	r, err := LoadRoster(rosterPath)
	if err != nil {
		return err
	}
	var all []Bypass
	for _, repo := range r.Repos {
		bs, err := DetectBypasses(repo.Name, repo.Path, since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", repo.Name, err)
			continue
		}
		all = append(all, bs...)
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"bypasses": all, "over_budget": OverBudget(all)})
	}
	if len(all) == 0 {
		fmt.Println("no bypasses detected")
		return nil
	}
	for _, b := range all {
		fmt.Printf("  %-22s %-16s %-9s %s\n", b.Repo, b.Kind, b.Commit, b.Detail)
	}
	over := OverBudget(all)
	if len(over) == 0 {
		fmt.Printf("\n%d bypass(es), none over the budget of %d\n", len(all), BypassBudget)
		return nil
	}
	fmt.Printf("\nOVER BUDGET (>%d) — delete the gate or automate it:\n", BypassBudget)
	for k, n := range over {
		fmt.Printf("  %s: %d\n", k, n)
	}
	return nil
}

// doADRDrift always succeeds. ADR 0001's standing rule is that a gate goes
// blocking only once its noise level is known, and a check that failed the PR
// on its own inability to run would be the first thing anyone bypassed.
func doADRDrift(dir, base string, asJSON bool) error {
	drifts, err := DetectDrift(dir, base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: adr-drift: %v\n", err)
		drifts = nil
	}
	if asJSON {
		if drifts == nil {
			drifts = []Drift{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(drifts)
	}
	if len(drifts) == 0 {
		fmt.Println("adr-drift: nothing to flag")
		return nil
	}
	// ::warning:: puts each flag in the PR checks UI, the same way bead-lint.sh
	// does. It is a note next to the diff, never a failure.
	for _, d := range drifts {
		fmt.Printf("::warning::adr-drift %s: %s — %s\n", d.Kind, d.Path, d.Detail)
	}
	fmt.Fprintf(os.Stderr, "\n%d possible undocumented decision(s). If one of them is a decision, "+
		"add an ADR (copy docs/adr/template.md); if not, ignore this — it never blocks.\n", len(drifts))
	return nil
}

func doDoctor(rosterPath, source string, fix, asJSON, self bool) error {
	var r Roster
	if self {
		// An instance checking its own drift has no roster — it IS one repo.
		// Role and skipped stages come from .flywheel/repo.json if present, so
		// a training project still declares it has no use for Operate.
		r = selfRoster()
	} else {
		var err error
		if r, err = LoadRoster(rosterPath); err != nil {
			return err
		}
	}
	var all []Diagnosis
	for _, repo := range r.Repos {
		if _, err := os.Stat(repo.Path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: not on this machine\n", repo.Name)
			continue
		}
		d := Diagnose(repo)
		all = append(all, d)
		if !asJSON {
			fmt.Println(d)
		}
		if fix && len(d.Missing) > 0 {
			if source == "" {
				return fmt.Errorf("-fix needs -source pointing at a template repo")
			}
			installed, err := Install(repo, source, d.Missing)
			if err != nil {
				return err
			}
			for _, p := range installed {
				fmt.Printf("    + installed %s\n", p)
			}
			for _, m := range d.Missing {
				if m.Manual {
					fmt.Printf("    ! %s needs hand adaptation — not installed (fw-fsa.7)\n", m.Path)
				}
			}
			if len(installed) == 0 {
				fmt.Println("    (nothing installable from the source)")
			}
		}
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(all)
	}
	return nil
}

func doRun(rosterPath string, execute bool, perBuilder time.Duration, onlyBead string, asJSON bool) error {
	r, err := LoadRoster(rosterPath)
	if err != nil {
		return err
	}
	OnlyBead = onlyBead
	defer func() { OnlyBead = "" }()
	plan, err := AllocateWithLoad(r, func(p string) bdClient {
		return bdClient{dir: p, run: execBD}
	}, ghReviewLoad, time.Now())
	if err != nil {
		return err
	}
	if plan.Stopped != "" {
		fmt.Printf("HALTED — kill switch set (%s)\n", plan.Stopped)
		return nil
	}
	if onlyBead != "" && len(plan.Assignments) == 0 {
		return fmt.Errorf("%s is not allocatable: %s", onlyBead, whyNotAllocatable(r, onlyBead))
	}
	if len(plan.Assignments) == 0 {
		fmt.Println("nothing to do")
		for _, d := range plan.Declined {
			fmt.Printf("  %-22s %s\n", d.Repo, d.Reason)
		}
		return nil
	}

	opts := DefaultRunOpts()
	opts.Execute = execute
	opts.PerBuilder = perBuilder
	opts.Concurrency = r.Caps.ConcurrentBuilders
	opts.Runner = r.Runner

	fmt.Printf("%d assignment(s), weight %d/%d", len(plan.Assignments), plan.Allocated+plan.InReview, plan.Budget)
	if !execute {
		fmt.Print(" — DRY RUN, pass -execute to spawn builders")
	}
	fmt.Println()
	for _, a := range plan.Assignments {
		fmt.Printf("  %-22s %-12s w%d → %s\n", a.Repo, a.Bead, a.Weight, a.Title)
	}
	fmt.Println()

	builders, err := Run(r, plan, opts)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(builders)
	}
	fmt.Println(Digest(builders))
	return nil
}

// whyNotAllocatable names the single reason a bead was excluded. Listing all
// six possibilities tells the reader nothing they can act on (fw-lb8.7).
func whyNotAllocatable(r Roster, id string) string {
	return whyNotAllocatableWith(r, id, func(p string) bdClient {
		return bdClient{dir: p, run: execBD}
	})
}

func whyNotAllocatableWith(r Roster, id string, open openClient) string {
	for _, repo := range r.Repos {
		b, err := open(repo.Path).show(id)
		if err != nil {
			continue
		}
		switch {
		case b.Status == "closed":
			return "it is already closed"
		case b.Status == "in_progress":
			if holder, exp, ok := b.Lease(); ok {
				return fmt.Sprintf("claimed by %s until %s", holder, exp.Format(time.RFC3339))
			}
			return "in progress with no lease — a human may hold it, or a builder died without releasing (reclaim will not touch it)"
		case b.Type == "epic":
			return "it is an epic, a container rather than work"
		case b.Type == "decision":
			return "it is a decision, which a human makes"
		case b.HasLabel("human-only"):
			return "it is labelled human-only"
		case Weight(b) > r.Caps.ReviewWeightPerNight:
			return fmt.Sprintf("weight %d exceeds the whole nightly budget of %d", Weight(b), r.Caps.ReviewWeightPerNight)
		}
		return "blocked by an open dependency or gate"
	}
	return "no roster repo has a bead with that id"
}

func defaultRoster() string {
	if p := os.Getenv("FLEET_ROSTER"); p != "" {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, "Workspace", "agentic-flywheel", ".flywheel", "roster.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ".flywheel/roster.json"
}

func usage() {
	fmt.Fprint(os.Stderr, `fleet — cross-repo agent coordinator

  fleet claim <bead> -agent NAME [-ttl 45m] [-dir .]
  fleet heartbeat <bead> -agent NAME [-ttl 45m] [-dir .]
  fleet release <bead> -agent NAME [-dir .]
  fleet reclaim [-dir .] [-json]
  fleet status [-roster PATH] [-json]
  fleet allocate [-roster PATH] [-json]
  fleet hydrate [-roster PATH] [-json]
  fleet bypasses [-roster PATH] [-since 30.days] [-json]
  fleet adr-drift [-dir .] [-base origin/main] [-json]
  fleet doctor [-roster PATH | -self] [-fix -source PATH] [-json]
  fleet run [-roster PATH] [-execute] [-bead ID] [-per-builder 25m] [-json]
`)
}
