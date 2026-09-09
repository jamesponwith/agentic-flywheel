package main

import (
	"errors"
	"strings"
	"testing"
)

func TestReconcileBoard(t *testing.T) {
	tests := []struct {
		name string
		// defaultBranch is the branch this repo ships from; "" means main.
		defaultBranch       string
		beads               []Bead
		prs                 []PR
		wantClosed          []string
		wantKept            []string
		wantElsewhere       []string
		wantClosedElsewhere []string
		wantDetail          string // substring of the single report, when set
		wantCalls           int    // bd close invocations
	}{
		{
			name:       "merged PR on the bead's branch closes an open bead",
			beads:      []Bead{{ID: "fw-d20", Status: "open"}},
			prs:        []PR{{Number: 62, State: "MERGED", Title: "one PR counts as one green", HeadRefName: "bead/fw-d20", BaseRefName: "main"}},
			wantClosed: []string{"fw-d20"},
			wantCalls:  1,
		},
		{
			name:       "merged PR closes an in-progress bead too — the builder stopped at PR-open, as told",
			beads:      []Bead{{ID: "fw-6gc", Status: "in_progress", Metadata: map[string]string{"lease_holder": "fleet/builder-go"}}},
			prs:        []PR{{Number: 64, State: "MERGED", Title: "fw-6gc: a byte-identical ledger line is one event", HeadRefName: "bead/fw-6gc", BaseRefName: "main"}},
			wantClosed: []string{"fw-6gc"},
			wantCalls:  1,
		},
		{
			name:       "a PR naming the bead only in its title still counts — human branches are not bead/",
			beads:      []Bead{{ID: "fw-0rf", Status: "open"}},
			prs:        []PR{{Number: 63, State: "MERGED", Title: "test fixtures must not resolve to real accounts (fw-0rf)", HeadRefName: "fixture-identity", BaseRefName: "main"}},
			wantClosed: []string{"fw-0rf"},
			wantCalls:  1,
		},
		{
			// The bug: spot-2ig was closed on a PR that merged into
			// fleet/builder-permissions. The board said done; main never had
			// the 1880 lines (fw-ojk).
			name:  "a PR merged into a feature branch is not a merge — the work is not on main",
			beads: []Bead{{ID: "spot-2ig", Status: "open"}},
			prs: []PR{{Number: 4, State: "MERGED", Title: "spot-2ig: lexicon audit",
				HeadRefName: "bead/spot-2ig", BaseRefName: "fleet/builder-permissions"}},
			wantElsewhere: []string{"spot-2ig"},
			wantDetail:    "merged into fleet/builder-permissions, not main",
			wantCalls:     0,
		},
		{
			// A PR merged into another bead's branch — the shape a stack takes
			// when the child is not retargeted to main before the parent lands.
			name:          "the branch it went to is named, so the work is findable rather than silently dropped",
			beads:         []Bead{{ID: "fw-aaa", Status: "in_progress"}},
			prs:           []PR{{Number: 46, State: "MERGED", Title: "fw-aaa: part two", HeadRefName: "bead/fw-aaa", BaseRefName: "bead/fw-parent"}},
			wantElsewhere: []string{"fw-aaa"},
			wantDetail:    "PR #46 merged into bead/fw-parent, not main",
			wantCalls:     0,
		},
		{
			// Not hardcoded to main: a repo shipping from master would have
			// every merge read as a detour, and nothing would ever close.
			name:          "the default branch comes from the repo — a master repo closes on master",
			defaultBranch: "master",
			beads:         []Bead{{ID: "fw-ddd", Status: "open"}},
			prs:           []PR{{Number: 80, State: "MERGED", Title: "fw-ddd: work", HeadRefName: "bead/fw-ddd", BaseRefName: "master"}},
			wantClosed:    []string{"fw-ddd"},
			wantCalls:     1,
		},
		{
			name:          "and main is a feature branch there — a merge into it does not close",
			defaultBranch: "master",
			beads:         []Bead{{ID: "fw-ddd", Status: "open"}},
			prs:           []PR{{Number: 81, State: "MERGED", Title: "fw-ddd: work", HeadRefName: "bead/fw-ddd", BaseRefName: "main"}},
			wantElsewhere: []string{"fw-ddd"},
			wantDetail:    "merged into main, not master",
			wantCalls:     0,
		},
		{
			// Something did land, so the bead closes — but the close says where
			// the rest went. A builder writes its own PR title, so a small PR
			// onto main titled after the bead would otherwise close it while
			// the work sits on a branch nobody merges (security lens).
			name:  "a detour beside a real landing closes, and the close names the detour",
			beads: []Bead{{ID: "fw-eee", Status: "open"}},
			prs: []PR{
				{Number: 90, State: "MERGED", Title: "fw-eee: first cut", HeadRefName: "bead/fw-eee", BaseRefName: "fleet/staging"},
				{Number: 91, State: "MERGED", Title: "fw-eee: the same work, onto main", HeadRefName: "fleet/staging", BaseRefName: "main"},
			},
			wantClosed: []string{"fw-eee"},
			wantDetail: "NOTE: PR #90 for this bead merged into fleet/staging, not main",
			wantCalls:  1,
		},
		{
			// The board's past: spot-2ig was closed on PR #4 before this rule
			// existed, and a fix that only prevents the next one leaves the
			// 1880 lines exactly as invisible (fw-ojk).
			name:  "a bead already closed on a detour is surfaced, not left buried",
			beads: []Bead{{ID: "spot-2ig", Status: "closed"}},
			prs: []PR{{Number: 4, State: "MERGED", Title: "spot-2ig: lexicon audit",
				HeadRefName: "bead/spot-2ig", BaseRefName: "fleet/builder-permissions"}},
			wantClosedElsewhere: []string{"spot-2ig"},
			wantDetail:          "closed, but PR #4 merged into fleet/builder-permissions, not main",
			wantCalls:           0,
		},
		{
			name:  "a bead closed on a PR that did reach main is not surfaced, detour or no detour",
			beads: []Bead{{ID: "fw-fff", Status: "closed"}},
			prs: []PR{
				{Number: 92, State: "MERGED", Title: "fw-fff: work", HeadRefName: "bead/fw-fff", BaseRefName: "main"},
				{Number: 93, State: "MERGED", Title: "someone else's detour", HeadRefName: "spike", BaseRefName: "fleet/staging"},
			},
			wantCalls: 0,
		},
		{
			name:      "an open PR is not a merge — the bead stays",
			beads:     []Bead{{ID: "fw-aaa", Status: "in_progress"}},
			prs:       []PR{{Number: 70, State: "OPEN", HeadRefName: "bead/fw-aaa", BaseRefName: "main"}},
			wantCalls: 0,
		},
		{
			name:      "a PR closed without merging is not a merge — the work is not on main",
			beads:     []Bead{{ID: "fw-aaa", Status: "open"}},
			prs:       []PR{{Number: 70, State: "CLOSED", HeadRefName: "bead/fw-aaa", BaseRefName: "main"}},
			wantCalls: 0,
		},
		{
			name:      "no PR at all — nothing to reconcile",
			beads:     []Bead{{ID: "fw-aaa", Status: "open"}},
			wantCalls: 0,
		},
		{
			name:  "a merged parent with an open child is a stack still in review — kept, not closed",
			beads: []Bead{{ID: "fw-bbb", Status: "in_progress"}},
			prs: []PR{
				{Number: 71, State: "MERGED", Title: "fw-bbb: part one", HeadRefName: "bead/fw-bbb", BaseRefName: "main"},
				{Number: 72, State: "OPEN", Title: "fw-bbb: part two", HeadRefName: "bead/fw-bbb-2", BaseRefName: "main"},
			},
			wantKept:  []string{"fw-bbb"},
			wantCalls: 0,
		},
		{
			name:      "fw-d20's merge must not close fw-d20.1 — ids nest",
			beads:     []Bead{{ID: "fw-d20.1", Status: "open"}},
			prs:       []PR{{Number: 62, State: "MERGED", Title: "one PR counts as one green (fw-d20)", HeadRefName: "bead/fw-d20", BaseRefName: "main"}},
			wantCalls: 0,
		},
		{
			name:      "fw-d20.1's merge must not close fw-d20 — the parent is not done because a child is",
			beads:     []Bead{{ID: "fw-d20", Status: "open"}},
			prs:       []PR{{Number: 65, State: "MERGED", Title: "fw-d20.1: the first child", HeadRefName: "bead/fw-d20.1", BaseRefName: "main"}},
			wantCalls: 0,
		},
		{
			name:      "a closed bead is not touched, even with a merged PR",
			beads:     []Bead{{ID: "fw-ccc", Status: "closed"}},
			prs:       []PR{{Number: 60, State: "MERGED", HeadRefName: "bead/fw-ccc", BaseRefName: "main"}},
			wantCalls: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			branch := tt.defaultBranch
			if branch == "" {
				branch = "main"
			}
			repo := Repo{Name: "scratch", Path: ".", DefaultBranch: branch}
			f := newFake(tt.beads...)
			got, err := ReconcileBoard(repo, bdClient{dir: ".", run: f.run}, func(Repo) ([]PR, error) { return tt.prs, nil }, true)
			if err != nil {
				t.Fatal(err)
			}
			var closed, kept, elsewhere, closedElsewhere []string
			for _, c := range got {
				switch c.Action {
				case "closed":
					closed = append(closed, c.Bead)
				case "kept":
					kept = append(kept, c.Bead)
				case "merged-elsewhere":
					elsewhere = append(elsewhere, c.Bead)
				case "closed-elsewhere":
					closedElsewhere = append(closedElsewhere, c.Bead)
				default:
					t.Errorf("unexpected action %+v", c)
				}
			}
			if strings.Join(closed, ",") != strings.Join(tt.wantClosed, ",") {
				t.Errorf("closed %v, want %v", closed, tt.wantClosed)
			}
			if strings.Join(kept, ",") != strings.Join(tt.wantKept, ",") {
				t.Errorf("kept %v, want %v", kept, tt.wantKept)
			}
			if strings.Join(elsewhere, ",") != strings.Join(tt.wantElsewhere, ",") {
				t.Errorf("merged-elsewhere %v, want %v", elsewhere, tt.wantElsewhere)
			}
			if strings.Join(closedElsewhere, ",") != strings.Join(tt.wantClosedElsewhere, ",") {
				t.Errorf("closed-elsewhere %v, want %v", closedElsewhere, tt.wantClosedElsewhere)
			}
			if tt.wantDetail != "" {
				if len(got) != 1 {
					t.Fatalf("want one report to inspect, got %+v", got)
				}
				if !strings.Contains(got[0].Detail, tt.wantDetail) {
					t.Errorf("detail %q does not say %q — the branch the work went to is how a human finds it",
						got[0].Detail, tt.wantDetail)
				}
			}
			calls := 0
			for _, c := range f.calls {
				if strings.HasPrefix(c, "close ") {
					calls++
				}
			}
			if calls != tt.wantCalls {
				t.Errorf("bd close called %d time(s), want %d:\n%s", calls, tt.wantCalls, strings.Join(f.calls, "\n"))
			}
			for _, id := range tt.wantClosed {
				if f.beads[id].Status != "closed" {
					t.Errorf("%s reported closed but is %s", id, f.beads[id].Status)
				}
				if !strings.Contains(f.beads[id].Metadata["close_reason"], "PR #") {
					t.Errorf("%s closed without naming the PR: %q", id, f.beads[id].Metadata["close_reason"])
				}
			}
		})
	}
}

// A listing failure is not an empty list. "gh is down" reported as "nothing
// merged" would dispatch builders onto finished work — the bug this fixes.
func TestReconcileBoardRefusesToGuessWhenGHFails(t *testing.T) {
	f := newFake(Bead{ID: "fw-d20", Status: "open"})
	_, err := ReconcileBoard(Repo{Name: "scratch", DefaultBranch: "main"}, bdClient{dir: ".", run: f.run},
		func(Repo) ([]PR, error) { return nil, errors.New("gh: connection refused") }, true)
	if err == nil {
		t.Fatal("a failed PR listing was reported as success")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error hides the cause: %v", err)
	}
	if f.beads["fw-d20"].Status != "open" {
		t.Errorf("bead was %s after a failed listing", f.beads["fw-d20"].Status)
	}
}

// Without a default branch there is no such thing as "merged": every PR would
// be either closed on or reported on by accident. Refuse, the same way a failed
// listing refuses, and close nothing (fw-ojk).
func TestReconcileBoardRefusesWithoutADefaultBranch(t *testing.T) {
	f := newFake(Bead{ID: "fw-d20", Status: "open"})
	_, err := ReconcileBoard(Repo{Name: "scratch"}, bdClient{dir: ".", run: f.run},
		func(Repo) ([]PR, error) {
			return []PR{{Number: 62, State: "MERGED", HeadRefName: "bead/fw-d20", BaseRefName: "main"}}, nil
		}, true)
	if err == nil {
		t.Fatal("reconciled against an unknown default branch")
	}
	if !strings.Contains(err.Error(), "default branch") {
		t.Errorf("error does not name the cause: %v", err)
	}
	if f.beads["fw-d20"].Status != "open" {
		t.Errorf("bead was %s after a refused reconcile", f.beads["fw-d20"].Status)
	}
}

// A MERGED PR with no base is gh not answering what it was asked. Absorbed, it
// would put every merge in the elsewhere bucket and close nothing — the fw-y1y
// drift again, wearing a per-bead disguise. So it refuses, loudly.
func TestReconcileBoardRefusesAMergedPRWithNoBase(t *testing.T) {
	f := newFake(Bead{ID: "fw-d20", Status: "open"})
	_, err := ReconcileBoard(Repo{Name: "scratch", DefaultBranch: "main"}, bdClient{dir: ".", run: f.run},
		func(Repo) ([]PR, error) {
			return []PR{{Number: 62, State: "MERGED", HeadRefName: "bead/fw-d20"}}, nil
		}, true)
	if err == nil {
		t.Fatal("a MERGED PR with no base was reconciled as if it had one")
	}
	if !strings.Contains(err.Error(), "#62") {
		t.Errorf("error does not name the PR: %v", err)
	}
	if f.beads["fw-d20"].Status != "open" {
		t.Errorf("bead was %s after a refused reconcile", f.beads["fw-d20"].Status)
	}
}

// Board lines are tee'd into the committed nightly digest, so a PR title with a
// newline in it could forge a second line — one saying a bead closed that
// nothing closed. Titles are written by agents; flatten them.
func TestReconcileBoardFlattensAPRTitle(t *testing.T) {
	f := newFake(Bead{ID: "fw-d20", Status: "open"})
	forged := "work\n  board scratch fw-zzz  closed  PR #7 merged: never happened"
	got, err := ReconcileBoard(Repo{Name: "scratch", DefaultBranch: "main"}, bdClient{dir: ".", run: f.run},
		func(Repo) ([]PR, error) {
			return []PR{{Number: 62, State: "MERGED", Title: forged, HeadRefName: "bead/fw-d20", BaseRefName: "main"}}, nil
		}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v, want one report", got)
	}
	if strings.ContainsAny(got[0].Detail, "\n\r") {
		t.Errorf("report carries a newline out of a PR title: %q", got[0].Detail)
	}
	if strings.ContainsAny(f.beads["fw-d20"].Metadata["close_reason"], "\n\r") {
		t.Errorf("close reason carries a newline out of a PR title: %q", f.beads["fw-d20"].Metadata["close_reason"])
	}
}

// A close that fails is reported as failed, and the rest of the board is still
// reconciled — one bad bead must not hide every other merge.
func TestReconcileBoardReportsAFailedClose(t *testing.T) {
	f := newFake(Bead{ID: "fw-aaa", Status: "open"}, Bead{ID: "fw-bbb", Status: "open"})
	f.fail["close fw-aaa"] = true
	got, err := ReconcileBoard(Repo{Name: "scratch", DefaultBranch: "main"}, bdClient{dir: ".", run: f.run}, func(Repo) ([]PR, error) {
		return []PR{
			{Number: 1, State: "MERGED", HeadRefName: "bead/fw-aaa", BaseRefName: "main"},
			{Number: 2, State: "MERGED", HeadRefName: "bead/fw-bbb", BaseRefName: "main"},
		}, nil
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Action != "failed" || got[1].Action != "closed" {
		t.Fatalf("got %+v, want fw-aaa failed and fw-bbb closed", got)
	}
}

// Without -execute the reconciler shows its hand and touches nothing — the
// same convention as run, so a human can see what a night would close first.
func TestReconcileBoardDryRunClosesNothing(t *testing.T) {
	f := newFake(Bead{ID: "fw-d20", Status: "open"})
	got, err := ReconcileBoard(Repo{Name: "scratch", DefaultBranch: "main"}, bdClient{dir: ".", run: f.run},
		func(Repo) ([]PR, error) {
			return []PR{{Number: 62, State: "MERGED", HeadRefName: "bead/fw-d20", BaseRefName: "main"}}, nil
		}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != "would-close" || got[0].PR != 62 {
		t.Fatalf("got %+v, want fw-d20 would-close on #62", got)
	}
	if f.beads["fw-d20"].Status != "open" {
		t.Errorf("dry run closed the bead")
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "close ") {
			t.Errorf("dry run called bd close: %s", c)
		}
	}
}

func TestNames(t *testing.T) {
	tests := []struct {
		name string
		pr   PR
		id   string
		want bool
	}{
		{"the spawner's branch", PR{HeadRefName: "bead/fw-d20"}, "fw-d20", true},
		{"a sibling's branch", PR{HeadRefName: "bead/fw-d20.1"}, "fw-d20", false},
		{"leading id", PR{Title: "fw-d20: cap builders", HeadRefName: "x"}, "fw-d20", true},
		{"trailing id", PR{Title: "one PR counts as one green (fw-d20) (#62)", HeadRefName: "x"}, "fw-d20", true},
		{"a child's leading id is not the parent's", PR{Title: "fw-d20.1: the first child"}, "fw-d20", false},
		{"the parent's trailing id is not the child's", PR{Title: "one PR counts as one green (fw-d20)"}, "fw-d20.1", false},
		{"mentioned mid-title — an agent could name any bead it likes there", PR{Title: "fw-abc: cap builders, also fw-def"}, "fw-def", false},
		{"bare id with no shape", PR{Title: "fw-d20"}, "fw-d20", false},
		{"prefix of a longer id", PR{Title: "fw-d200: something else"}, "fw-d20", false},
		{"a revert quotes the original and means the opposite", PR{Title: `Revert "fw-d20: cap builders"`, HeadRefName: "revert-62"}, "fw-d20", false},
		{"a revert with the trailing shape", PR{Title: `Revert "one PR counts as one green (fw-d20)"`}, "fw-d20", false},
		{"empty title", PR{}, "fw-d20", false},
	}
	for _, tt := range tests {
		if got := names(tt.pr, tt.id); got != tt.want {
			t.Errorf("%s: names(%+v, %q) = %v, want %v", tt.name, tt.pr, tt.id, got, tt.want)
		}
	}
}
