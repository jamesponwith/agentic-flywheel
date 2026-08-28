package main

import (
	"errors"
	"strings"
	"testing"
)

func TestReconcileBoard(t *testing.T) {
	repo := Repo{Name: "scratch", Path: "."}
	tests := []struct {
		name       string
		beads      []Bead
		prs        []PR
		wantClosed []string
		wantKept   []string
		wantCalls  int // bd close invocations
	}{
		{
			name:       "merged PR on the bead's branch closes an open bead",
			beads:      []Bead{{ID: "fw-d20", Status: "open"}},
			prs:        []PR{{Number: 62, State: "MERGED", Title: "one PR counts as one green", HeadRefName: "bead/fw-d20"}},
			wantClosed: []string{"fw-d20"},
			wantCalls:  1,
		},
		{
			name:       "merged PR closes an in-progress bead too — the builder stopped at PR-open, as told",
			beads:      []Bead{{ID: "fw-6gc", Status: "in_progress", Metadata: map[string]string{"lease_holder": "fleet/builder-go"}}},
			prs:        []PR{{Number: 64, State: "MERGED", Title: "fw-6gc: a byte-identical ledger line is one event", HeadRefName: "bead/fw-6gc"}},
			wantClosed: []string{"fw-6gc"},
			wantCalls:  1,
		},
		{
			name:       "a PR naming the bead only in its title still counts — human branches are not bead/",
			beads:      []Bead{{ID: "fw-0rf", Status: "open"}},
			prs:        []PR{{Number: 63, State: "MERGED", Title: "test fixtures must not resolve to real accounts (fw-0rf)", HeadRefName: "fixture-identity"}},
			wantClosed: []string{"fw-0rf"},
			wantCalls:  1,
		},
		{
			name:      "an open PR is not a merge — the bead stays",
			beads:     []Bead{{ID: "fw-aaa", Status: "in_progress"}},
			prs:       []PR{{Number: 70, State: "OPEN", HeadRefName: "bead/fw-aaa"}},
			wantCalls: 0,
		},
		{
			name:      "a PR closed without merging is not a merge — the work is not on main",
			beads:     []Bead{{ID: "fw-aaa", Status: "open"}},
			prs:       []PR{{Number: 70, State: "CLOSED", HeadRefName: "bead/fw-aaa"}},
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
				{Number: 71, State: "MERGED", Title: "fw-bbb: part one", HeadRefName: "bead/fw-bbb"},
				{Number: 72, State: "OPEN", Title: "fw-bbb: part two", HeadRefName: "bead/fw-bbb-2"},
			},
			wantKept:  []string{"fw-bbb"},
			wantCalls: 0,
		},
		{
			name:      "fw-d20's merge must not close fw-d20.1 — ids nest",
			beads:     []Bead{{ID: "fw-d20.1", Status: "open"}},
			prs:       []PR{{Number: 62, State: "MERGED", Title: "one PR counts as one green (fw-d20)", HeadRefName: "bead/fw-d20"}},
			wantCalls: 0,
		},
		{
			name:      "fw-d20.1's merge must not close fw-d20 — the parent is not done because a child is",
			beads:     []Bead{{ID: "fw-d20", Status: "open"}},
			prs:       []PR{{Number: 65, State: "MERGED", Title: "fw-d20.1: the first child", HeadRefName: "bead/fw-d20.1"}},
			wantCalls: 0,
		},
		{
			name:      "a closed bead is not touched, even with a merged PR",
			beads:     []Bead{{ID: "fw-ccc", Status: "closed"}},
			prs:       []PR{{Number: 60, State: "MERGED", HeadRefName: "bead/fw-ccc"}},
			wantCalls: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(tt.beads...)
			got, err := ReconcileBoard(repo, bdClient{dir: ".", run: f.run}, func(Repo) ([]PR, error) { return tt.prs, nil }, true)
			if err != nil {
				t.Fatal(err)
			}
			var closed, kept []string
			for _, c := range got {
				switch c.Action {
				case "closed":
					closed = append(closed, c.Bead)
				case "kept":
					kept = append(kept, c.Bead)
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
	_, err := ReconcileBoard(Repo{Name: "scratch"}, bdClient{dir: ".", run: f.run},
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

// A close that fails is reported as failed, and the rest of the board is still
// reconciled — one bad bead must not hide every other merge.
func TestReconcileBoardReportsAFailedClose(t *testing.T) {
	f := newFake(Bead{ID: "fw-aaa", Status: "open"}, Bead{ID: "fw-bbb", Status: "open"})
	f.fail["close fw-aaa"] = true
	got, err := ReconcileBoard(Repo{Name: "scratch"}, bdClient{dir: ".", run: f.run}, func(Repo) ([]PR, error) {
		return []PR{
			{Number: 1, State: "MERGED", HeadRefName: "bead/fw-aaa"},
			{Number: 2, State: "MERGED", HeadRefName: "bead/fw-bbb"},
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
	got, err := ReconcileBoard(Repo{Name: "scratch"}, bdClient{dir: ".", run: f.run},
		func(Repo) ([]PR, error) { return []PR{{Number: 62, State: "MERGED", HeadRefName: "bead/fw-d20"}}, nil }, false)
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

func TestMentions(t *testing.T) {
	tests := []struct {
		s, id string
		want  bool
	}{
		{"fw-d20: cap builders", "fw-d20", true},
		{"one PR counts as one green (fw-d20) (#62)", "fw-d20", true},
		{"closes fw-d20.", "fw-d20", true}, // sentence-ending full stop
		{"fw-d20", "fw-d20", true},
		{"fw-d20.1: the first child", "fw-d20", false},
		{"one PR counts as one green (fw-d20)", "fw-d20.1", false},
		{"afw-d20", "fw-d20", false},
		{"fw-d200", "fw-d20", false},
		{"fw-d2", "fw-d20", false},
		{"", "fw-d20", false},
	}
	for _, tt := range tests {
		if got := mentions(tt.s, tt.id); got != tt.want {
			t.Errorf("mentions(%q, %q) = %v, want %v", tt.s, tt.id, got, tt.want)
		}
	}
}
