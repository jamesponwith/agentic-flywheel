package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func testRoster() Roster {
	return Roster{
		Caps: Caps{ReviewWeightPerNight: 8, ConcurrentBuilders: 3, ReposPerNight: 2},
		Repos: []Repo{
			{Name: "router", Path: "/r/router", Lang: "go"},
			{Name: "brax", Path: "/r/brax", Lang: "python"},
			{Name: "third", Path: "/r/third", Lang: "go"},
		},
		Agents: []Agent{
			{Name: "fleet/builder-go", Role: "builder", Repos: []string{"*"}, Skills: []string{"go"}},
			{Name: "fleet/builder-py", Role: "builder", Repos: []string{"brax"}, Skills: []string{"python", "jax"}},
			{Name: "fleet/reviewer", Role: "reviewer", Repos: []string{"*"}},
		},
	}
}

func fixtures(m map[string][]Bead) openClient {
	return func(path string) bdClient {
		f := newFake(m[path]...)
		return bdClient{dir: path, run: f.run}
	}
}

func TestAllocate(t *testing.T) {
	task := func(id string, prio int, labels ...string) Bead {
		return Bead{ID: id, Status: "open", Priority: prio, Type: "task", Title: id, Labels: labels}
	}

	tests := []struct {
		name        string
		roster      func() Roster
		repos       map[string][]Bead
		wantAssign  []string // "repo:bead:agent"
		wantDecline []string // substrings expected among decline reasons
	}{
		{
			name:   "routes by skill label and orders by priority",
			roster: testRoster,
			repos: map[string][]Bead{
				"/r/router": {task("r-2", 2, "lang:go"), task("r-1", 0, "lang:go")},
			},
			wantAssign: []string{"router:r-1:fleet/builder-go", "router:r-2:fleet/builder-go"},
		},
		{
			name:   "python bead goes to the python builder",
			roster: testRoster,
			repos: map[string][]Bead{
				"/r/brax": {task("b-1", 1, "lang:python", "skill:jax")},
			},
			wantAssign: []string{"brax:b-1:fleet/builder-py"},
		},
		{
			name:   "bead needing a skill nobody has is declined with a reason",
			roster: testRoster,
			repos: map[string][]Bead{
				"/r/router": {task("r-1", 1, "lang:rust")},
			},
			wantDecline: []string{"no builder in the roster has the required skills"},
		},
		{
			name:   "epics, decisions and human-only beads are never allocated",
			roster: testRoster,
			repos: map[string][]Bead{
				"/r/router": {
					{ID: "e-1", Status: "open", Type: "epic", Labels: []string{"lang:go"}},
					{ID: "d-1", Status: "open", Type: "decision", Labels: []string{"lang:go"}},
					task("h-1", 1, "lang:go", "human-only"),
				},
			},
			wantAssign: nil,
		},
		{
			name:   "a bead under a live lease is skipped",
			roster: testRoster,
			repos: map[string][]Bead{
				"/r/router": {{ID: "r-1", Status: "open", Type: "task", Labels: []string{"lang:go"},
					Metadata: map[string]string{
						leaseHolderKey:  "someone",
						leaseExpiresKey: base.Add(time.Hour).Format(time.RFC3339)}}},
			},
			wantAssign: nil,
		},
		{
			name: "paused repo is declined, not silently skipped",
			roster: func() Roster {
				r := testRoster()
				r.Repos[0].Paused = true
				return r
			},
			repos:       map[string][]Bead{"/r/router": {task("r-1", 1, "lang:go")}},
			wantDecline: []string{"repo paused in roster"},
		},
		{
			name:   "concurrent builder cap bounds one repo",
			roster: testRoster,
			repos: map[string][]Bead{
				"/r/router": {task("r-1", 1, "lang:go"), task("r-2", 1, "lang:go"),
					task("r-3", 1, "lang:go"), task("r-4", 1, "lang:go")},
			},
			wantAssign:  []string{"router:r-1:fleet/builder-go", "router:r-2:fleet/builder-go", "router:r-3:fleet/builder-go"},
			wantDecline: []string{"concurrent builder cap reached (3)"},
		},
		{
			name:   "third repo declined once the repos-per-night cap is hit",
			roster: testRoster,
			repos: map[string][]Bead{
				"/r/router": {task("r-1", 1, "lang:go")},
				"/r/brax":   {task("b-1", 1, "lang:python", "skill:jax")},
				"/r/third":  {task("t-1", 1, "lang:go")},
			},
			wantAssign:  []string{"router:r-1:fleet/builder-go", "brax:b-1:fleet/builder-py"},
			wantDecline: []string{"repo cap reached (2 active repos/night)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Allocate(tt.roster(), fixtures(tt.repos), base)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, a := range plan.Assignments {
				got = append(got, a.Repo+":"+a.Bead+":"+a.Agent)
			}
			if len(got) != len(tt.wantAssign) {
				t.Fatalf("assignments = %v, want %v", got, tt.wantAssign)
			}
			for i := range got {
				if got[i] != tt.wantAssign[i] {
					t.Errorf("assignment[%d] = %q, want %q", i, got[i], tt.wantAssign[i])
				}
			}
			for _, want := range tt.wantDecline {
				found := false
				for _, d := range plan.Declined {
					if strings.Contains(d.Reason, want) {
						found = true
					}
				}
				if !found {
					t.Errorf("no decline reason containing %q; got %+v", want, plan.Declined)
				}
			}
		})
	}
}

func TestAllocateNeverExceedsNightlyPRCap(t *testing.T) {
	r := testRoster()
	r.Caps.ReviewWeightPerNight = 2
	r.Caps.ReposPerNight = 3
	repos := map[string][]Bead{}
	for _, p := range []string{"/r/router", "/r/brax", "/r/third"} {
		lab := "lang:go"
		if p == "/r/brax" {
			lab = "lang:python"
		}
		repos[p] = []Bead{{ID: p + "-1", Status: "open", Type: "task", Priority: 1, Labels: []string{lab, "skill:jax"}}}
	}
	plan, err := Allocate(r, fixtures(repos), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Assignments) > 2 {
		t.Fatalf("allocated %d PRs, cap is 2 — the review morning is the constraint (ADR 0006)", len(plan.Assignments))
	}
	if len(plan.Declined) == 0 {
		t.Error("hit the cap but declined nothing — silent truncation reads as 'nothing to do'")
	}
}

func TestLoadRosterRejectsMissingCaps(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/roster.json"
	if err := writeFile(p, `{"repos":[],"agents":[]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRoster(p); err == nil || !strings.Contains(err.Error(), "ADR 0006") {
		t.Fatalf("err = %v, want a complaint about caps", err)
	}
}

func TestAllocateSurvivesAnUnreachableRepo(t *testing.T) {
	// One broken repo must not sink the whole night — it is declined with the
	// error and the other repos still get work.
	open := func(path string) bdClient {
		if path == "/r/router" {
			return bdClient{dir: path, run: func(string, ...string) ([]byte, error) {
				return nil, errNotFound{"no such database"}
			}}
		}
		f := newFake(Bead{ID: "b-1", Status: "open", Type: "task", Priority: 1,
			Labels: []string{"lang:python", "skill:jax"}})
		return bdClient{dir: path, run: f.run}
	}
	plan, err := Allocate(testRoster(), open, base)
	if err != nil {
		t.Fatalf("one bad repo returned a hard error: %v", err)
	}
	if len(plan.Assignments) != 1 || plan.Assignments[0].Repo != "brax" {
		t.Fatalf("assignments = %+v, want brax still allocated", plan.Assignments)
	}
	found := false
	for _, d := range plan.Declined {
		if d.Repo == "router" && strings.Contains(d.Reason, "bd ready failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("broken repo not reported; declines = %+v", plan.Declined)
	}
}

func TestAllocateHaltsOnKillSwitch(t *testing.T) {
	// A repo-local STOP file must halt the whole cycle, not just that repo:
	// half a fleet is a worse state than a stopped one.
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/.flywheel", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(dir+"/.flywheel/STOP", "drill"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLYWHEEL_HOME", t.TempDir()) // no fleet-wide stop
	r := testRoster()
	r.Repos[0].Path = dir

	plan, err := Allocate(r, fixtures(map[string][]Bead{
		dir: {{ID: "r-1", Status: "open", Type: "task", Priority: 1, Labels: []string{"lang:go"}}},
	}), base)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stopped == "" {
		t.Fatal("kill switch was set but the cycle allocated anyway")
	}
	if len(plan.Assignments) != 0 {
		t.Errorf("allocated %d beads while stopped", len(plan.Assignments))
	}
}

func TestAllocateIsDeterministic(t *testing.T) {
	// Equal-priority beads must break ties by id, so the same queue always
	// produces the same plan. Reproducibility is what makes a plan reviewable.
	beads := []Bead{
		{ID: "r-4", Status: "open", Type: "task", Priority: 1, Labels: []string{"lang:go"}},
		{ID: "r-1", Status: "open", Type: "task", Priority: 1, Labels: []string{"lang:go"}},
		{ID: "r-3", Status: "open", Type: "task", Priority: 1, Labels: []string{"lang:go"}},
		{ID: "r-2", Status: "open", Type: "task", Priority: 1, Labels: []string{"lang:go"}},
	}
	t.Setenv("FLYWHEEL_HOME", t.TempDir())
	want := []string{"r-1", "r-2", "r-3"} // cap is 3 concurrent builders
	for run := 0; run < 20; run++ {
		plan, err := Allocate(testRoster(), fixtures(map[string][]Bead{"/r/router": beads}), base)
		if err != nil {
			t.Fatal(err)
		}
		for i := range want {
			if plan.Assignments[i].Bead != want[i] {
				t.Fatalf("run %d: assignment[%d] = %s, want %s — plan is not reproducible",
					run, i, plan.Assignments[i].Bead, want[i])
			}
		}
	}
}

func TestWeight(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   int
	}{
		{"unlabelled is ordinary work", nil, DefaultWeight},
		{"trivial", []string{"w:1"}, 1},
		{"ordinary", []string{"w:2"}, 2},
		{"hard", []string{"w:3"}, 3},
		{"out of range is clamped, not rejected", []string{"w:9"}, DefaultWeight},
		{"nonsense is clamped", []string{"w:big"}, DefaultWeight},
		{"other labels ignored", []string{"lang:go", "w:1"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Weight(Bead{Labels: tt.labels}); got != tt.want {
				t.Errorf("Weight(%v) = %d, want %d", tt.labels, got, tt.want)
			}
		})
	}
}

func TestAllocateBudgetsWeightNotCount(t *testing.T) {
	// The ADR 0009 case: eight trivia or two hard changes, whichever the queue
	// holds. A flat PR count could express neither.
	t.Setenv("FLYWHEEL_HOME", t.TempDir())
	r := testRoster()
	r.Caps.ReviewWeightPerNight = 6
	r.Caps.ConcurrentBuilders = 99
	r.Caps.ReposPerNight = 9

	light := []Bead{}
	for i := 0; i < 8; i++ {
		light = append(light, Bead{ID: fmt.Sprintf("l-%d", i), Status: "open", Type: "task",
			Priority: 1, Labels: []string{"lang:go", "w:1"}})
	}
	plan, err := Allocate(r, fixtures(map[string][]Bead{"/r/router": light}), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Assignments) != 6 || plan.Allocated != 6 {
		t.Errorf("trivia: %d PRs / weight %d, want 6/6", len(plan.Assignments), plan.Allocated)
	}

	heavy := []Bead{}
	for i := 0; i < 8; i++ {
		heavy = append(heavy, Bead{ID: fmt.Sprintf("h-%d", i), Status: "open", Type: "task",
			Priority: 1, Labels: []string{"lang:go", "w:3"}})
	}
	plan, err = Allocate(r, fixtures(map[string][]Bead{"/r/router": heavy}), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Assignments) != 2 || plan.Allocated != 6 {
		t.Errorf("hard changes: %d PRs / weight %d, want 2/6", len(plan.Assignments), plan.Allocated)
	}
}

func TestAllocateSkipsTooHeavyButKeepsLookingForLighterWork(t *testing.T) {
	// Budget left: 2. A w:3 bead cannot fit, but a w:1 behind it can — breaking
	// out of the loop there would waste the remaining budget on nothing.
	t.Setenv("FLYWHEEL_HOME", t.TempDir())
	r := testRoster()
	r.Caps.ReviewWeightPerNight = 2
	plan, err := Allocate(r, fixtures(map[string][]Bead{"/r/router": {
		{ID: "a-heavy", Status: "open", Type: "task", Priority: 1, Labels: []string{"lang:go", "w:3"}},
		{ID: "b-light", Status: "open", Type: "task", Priority: 1, Labels: []string{"lang:go", "w:1"}},
	}}), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Assignments) != 1 || plan.Assignments[0].Bead != "b-light" {
		t.Fatalf("assignments = %+v, want just b-light", plan.Assignments)
	}
	found := false
	for _, d := range plan.Declined {
		if d.Bead == "a-heavy" && strings.Contains(d.Reason, "exceeds") {
			found = true
		}
	}
	if !found {
		t.Errorf("the too-heavy bead was not declined with a reason: %+v", plan.Declined)
	}
}

func TestAllocateIsPressureSensitive(t *testing.T) {
	// A full review queue reduces what tonight may start. This is the
	// constraint enforcing itself rather than being remembered (fw-o28).
	t.Setenv("FLYWHEEL_HOME", t.TempDir())
	r := testRoster()
	r.Caps.ReviewWeightPerNight = 8

	beads := map[string][]Bead{"/r/router": {
		{ID: "r-1", Status: "open", Type: "task", Priority: 1, Labels: []string{"lang:go", "w:3"}},
	}}

	// Queue already full: nothing starts.
	plan, err := AllocateWithLoad(r, fixtures(beads), func(Repo) int { return 8 }, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Assignments) != 0 {
		t.Errorf("started work with a full review queue: %+v", plan.Assignments)
	}
	if len(plan.Declined) == 0 || !strings.Contains(plan.Declined[0].Reason, "review queue is full") {
		t.Errorf("did not explain why nothing started: %+v", plan.Declined)
	}

	// Queue clear: the same bead is allocatable.
	plan, err = AllocateWithLoad(r, fixtures(beads), func(Repo) int { return 0 }, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Assignments) != 1 {
		t.Fatalf("clear queue allocated %d, want 1", len(plan.Assignments))
	}
	if plan.InReview != 0 || plan.Budget != 8 {
		t.Errorf("accounting wrong: inReview=%d budget=%d", plan.InReview, plan.Budget)
	}
}

func TestAllocateReportsEveryRepoExactlyOnce(t *testing.T) {
	// fw-oef.11: a repo with no ready work was invisible, and silence reads as
	// "nothing to say" when it should read as "queue empty here".
	t.Setenv("FLYWHEEL_HOME", t.TempDir())
	r := testRoster()
	plan, err := Allocate(r, fixtures(map[string][]Bead{
		"/r/router": {{ID: "r-1", Status: "open", Type: "task", Priority: 1, Labels: []string{"lang:go"}}},
	}), base)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, a := range plan.Assignments {
		seen[a.Repo]++
	}
	for _, d := range plan.Declined {
		if d.Repo != "" {
			seen[d.Repo]++
		}
	}
	for _, repo := range r.Repos {
		if seen[repo.Name] == 0 {
			t.Errorf("%s appeared nowhere in the plan", repo.Name)
		}
	}
}

func TestLoadRosterMigratesPRsPerNightToWeight(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/roster.json"
	if err := writeFile(p, `{"caps":{"prs_per_night":6,"concurrent_builders":3,"repos_per_night":2},
	  "repos":[],"agents":[]}`); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRoster(p)
	if err != nil {
		t.Fatalf("an ADR 0006-era roster should still load: %v", err)
	}
	if r.Caps.ReviewWeightPerNight != 6 {
		t.Errorf("migrated budget = %d, want 6", r.Caps.ReviewWeightPerNight)
	}
}

func TestAllocateRefusesAZeroBudget(t *testing.T) {
	// A scheduler that silently does no work looks identical to one with an
	// empty queue. Found by a test that built Caps directly and bypassed the
	// migration in LoadRoster.
	t.Setenv("FLYWHEEL_HOME", t.TempDir())
	r := testRoster()
	r.Caps = Caps{ConcurrentBuilders: 3, ReposPerNight: 2} // no budget at all
	_, err := Allocate(r, fixtures(nil), base)
	if err == nil {
		t.Fatal("allocated with no budget instead of refusing")
	}
	if !strings.Contains(err.Error(), "ADR 0009") {
		t.Errorf("error does not point at the decision: %v", err)
	}
}

func TestCapsNormalizeMigratesInCode(t *testing.T) {
	// A Roster built in code never passes through LoadRoster.
	got := Caps{PRsPerNight: 6, ConcurrentBuilders: 3, ReposPerNight: 2}.normalized()
	if got.ReviewWeightPerNight != 6 {
		t.Errorf("in-code migration failed: %+v", got)
	}
}

func TestWhyNotAllocatableNamesTheReason(t *testing.T) {
	// "blocked, gated, claimed, human-only, an epic, or over the weight budget"
	// tells the reader nothing they can act on. One bead, one reason (fw-lb8.7).
	base := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		bead Bead
		want string
	}{
		{"closed", Bead{ID: "x", Status: "closed"}, "already closed"},
		{"epic", Bead{ID: "x", Status: "open", Type: "epic"}, "an epic"},
		{"decision", Bead{ID: "x", Status: "open", Type: "decision"}, "a decision"},
		{"human-only", Bead{ID: "x", Status: "open", Type: "task", Labels: []string{"human-only"}}, "human-only"},
		{"leased", Bead{ID: "x", Status: "in_progress", Metadata: map[string]string{
			leaseHolderKey: "a/b", leaseExpiresKey: base.Add(time.Hour).Format(time.RFC3339)}}, "claimed by a/b"},
		{"in progress, no lease", Bead{ID: "x", Status: "in_progress"}, "no lease"},
		{"too heavy", Bead{ID: "x", Status: "open", Type: "task", Labels: []string{"w:3"}}, "exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(tt.bead)
			r := Roster{Caps: Caps{ReviewWeightPerNight: 2}, Repos: []Repo{{Name: "r", Path: "/r"}}}
			_ = f
			got := whyNotAllocatableWith(r, "x", func(string) bdClient {
				return bdClient{dir: "/r", run: f.run}
			})
			if !strings.Contains(got, tt.want) {
				t.Errorf("reason = %q, want it to mention %q", got, tt.want)
			}
		})
	}
}
