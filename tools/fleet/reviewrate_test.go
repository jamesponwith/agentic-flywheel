package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// finding builds one ledger line with the given disposition. The other fields
// are fixed because none of them affect the arithmetic under test.
func finding(disposition string) string {
	return `{"ts":"2026-08-17T01:32:34Z","commit":"6c77519","branch":"bead/fw-x","repo":"r",` +
		`"lens":"correctness","file":"a.go","line":"1","severity":"medium",` +
		`"claim":"a claim","disposition":"` + disposition + `"}`
}

func ledgerRepo(t *testing.T, lines ...string) Repo {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, ".flywheel", "review.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Repo{Name: "r", Path: dir}
}

func dispositions(t *testing.T, counts map[string]int) []ReviewFinding {
	t.Helper()
	var lines []string
	for _, d := range []string{"accepted", "rejected", "ignored", "reviewed-later", ""} {
		for i := 0; i < counts[d]; i++ {
			lines = append(lines, finding(d))
		}
	}
	repo := ledgerRepo(t, lines...)
	fs, err := readLedger(filepath.Join(repo.Path, ".flywheel", "review.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestRate(t *testing.T) {
	tests := []struct {
		name   string
		counts map[string]int
		want   Rate
		// wantWhy is a substring the explanation must carry, so a caller is
		// told why there is no number rather than just that there isn't one.
		wantWhy string
	}{
		{
			// The headline refusal. Nobody judged anything, so there is no
			// evidence either way — and 0% would read as "the reviewer is
			// always wrong", which the ledger does not say.
			name:    "all ignored is unmeasurable, not zero percent",
			counts:  map[string]int{"ignored": 20},
			want:    Rate{Ignored: 20, Total: 20},
			wantWhy: "none accepted or rejected",
		},
		{
			// The mirror image: an all-accepted ledger below the sample floor
			// must not report 100% either. Flattering the reviewer is the same
			// error as maligning it.
			name:    "all accepted below the floor is unmeasurable, not one hundred percent",
			counts:  map[string]int{"accepted": 4},
			want:    Rate{Accepted: 4, Total: 4},
			wantWhy: "only 4 of 4 finding(s) judged",
		},
		{
			// Rejected and ignored are distinct buckets. If they were summed,
			// this would be 6 of one and none of the other.
			name:   "rejected and ignored stay distinct",
			counts: map[string]int{"rejected": 2, "ignored": 4},
			want:   Rate{Rejected: 2, Ignored: 4, Total: 6},
		},
		{
			// Precision divides by judged findings only. Were the 90 ignored
			// in the denominator this would be 8%, not 80%.
			name:   "precision ignores the ignored",
			counts: map[string]int{"accepted": 8, "rejected": 2, "ignored": 90},
			want: Rate{
				Accepted: 8, Rejected: 2, Ignored: 90, Total: 100,
				Precision: 0.8, Measurable: true,
			},
		},
		{
			name:   "exactly at the floor measures",
			counts: map[string]int{"accepted": 5, "rejected": 5},
			want: Rate{
				Accepted: 5, Rejected: 5, Total: 10,
				Precision: 0.5, Measurable: true,
			},
		},
		{
			name:    "one short of the floor does not",
			counts:  map[string]int{"accepted": 5, "rejected": 4},
			want:    Rate{Accepted: 5, Rejected: 4, Total: 9},
			wantWhy: "only 9 of 9 finding(s) judged",
		},
		{
			// An unrecognised disposition is counted in Total and in no
			// bucket. Folding it into "ignored" would be the same mistake as
			// folding "ignored" into "rejected", one layer down.
			name:    "unknown dispositions land in no bucket",
			counts:  map[string]int{"accepted": 1, "reviewed-later": 2, "": 1},
			want:    Rate{Accepted: 1, Total: 4},
			wantWhy: "only 1 of 4 finding(s) judged",
		},
		{
			name:    "an empty ledger has nothing to say",
			counts:  nil,
			want:    Rate{},
			wantWhy: "no reviews recorded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rate(dispositions(t, tt.counts))
			got.Repo = "r"

			counts := got
			counts.Why, counts.Repo = "", "" // compared separately
			if counts != tt.want {
				t.Errorf("rate = %+v, want %+v", counts, tt.want)
			}
			if tt.wantWhy != "" && !strings.Contains(got.Why, tt.wantWhy) {
				t.Errorf("Why = %q, want it to mention %q", got.Why, tt.wantWhy)
			}
			// No unmeasurable rate may ever render a percentage. A "0%" or
			// "100%" here is a manufactured number, which is the whole thing
			// this command exists not to do.
			if !got.Measurable && strings.Contains(got.String(), "%") {
				t.Errorf("unmeasurable rate rendered a percentage: %q", got.String())
			}
			if !got.Measurable && got.Precision != 0 {
				t.Errorf("Precision = %v on an unmeasurable rate; a caller may print it", got.Precision)
			}
		})
	}
}

func TestReadLedgerMissingFileIsNotAnError(t *testing.T) {
	// A repo nobody has reviewed must report "no reviews recorded" rather than
	// failing. The fleet has repos that have never had a review run.
	fs, err := readLedger(filepath.Join(t.TempDir(), "nope", "review.jsonl"))
	if err != nil {
		t.Fatalf("missing ledger returned an error: %v", err)
	}
	r := rate(fs)
	if r.Measurable || r.Total != 0 {
		t.Errorf("rate = %+v, want an empty unmeasurable rate", r)
	}
	if r.Why != "no reviews recorded" {
		t.Errorf("Why = %q, want %q", r.Why, "no reviews recorded")
	}
}

func TestReadLedgerUnreadableFileIsAnError(t *testing.T) {
	// The counterpart: a ledger that exists and cannot be read is a broken
	// thing pretending to be an empty one, and must not be silently reported
	// as "no reviews recorded".
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "review.jsonl")
	if err := os.WriteFile(path, []byte(finding("accepted")+"\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := readLedger(path); err == nil {
		t.Error("an unreadable ledger read as empty")
	}
}

func TestReadLedgerToleratesBlankAndCorruptLines(t *testing.T) {
	// The ledger is appended to by concurrent builders, so a torn write is a
	// question of when, not if — and one bad line must not lose the rest.
	repo := ledgerRepo(t,
		finding("accepted"),
		"",
		`{"ts":"2026-08-17T01:32:3`,
		"   ",
		finding("rejected"),
	)
	fs, err := readLedger(filepath.Join(repo.Path, ".flywheel", "review.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 {
		t.Fatalf("read %d finding(s), want 2 — a corrupt or blank line lost real entries", len(fs))
	}
	if fs[0].Disposition != "accepted" || fs[1].Disposition != "rejected" {
		t.Errorf("dispositions = %q/%q, want accepted/rejected", fs[0].Disposition, fs[1].Disposition)
	}
}

func TestReadLedgerParsesEveryField(t *testing.T) {
	// The struct is the ledger's schema. A field that silently stops parsing
	// would drop evidence without anyone noticing.
	repo := ledgerRepo(t, finding("accepted"))
	fs, err := readLedger(filepath.Join(repo.Path, ".flywheel", "review.jsonl"))
	if err != nil || len(fs) != 1 {
		t.Fatalf("read = %v, %v", fs, err)
	}
	want := ReviewFinding{
		TS: "2026-08-17T01:32:34Z", Commit: "6c77519", Branch: "bead/fw-x", Repo: "r",
		Lens: "correctness", File: "a.go", Line: "1", Severity: "medium",
		Claim: "a claim", Disposition: "accepted",
	}
	if fs[0] != want {
		t.Errorf("finding = %+v, want %+v", fs[0], want)
	}
}

// rosterFor writes a one-repo roster pointing at dir, so doReviewRate can be
// driven end to end without the machine's real roster.
func rosterFor(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roster.json")
	body := `{"caps":{"review_weight_per_night":8,"concurrent_builders":3,"repos_per_night":2},` +
		`"repos":[{"name":"r","path":` + strconv.Quote(dir) + `,"lang":"go"}],"agents":[]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReviewRateReportsUnreadableAsUnknownNotUnreviewed(t *testing.T) {
	// The command's whole argument is that it does not manufacture numbers. A
	// ledger that exists and cannot be read, reported as "no reviews
	// recorded", would be exactly that: an unknown rendered as a clean bill.
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	repo := ledgerRepo(t, finding("accepted"))
	if err := os.Chmod(filepath.Join(repo.Path, ".flywheel", "review.jsonl"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := doReviewRate(rosterFor(t, repo.Path), "", false); err == nil {
		t.Error("an unreadable ledger exited zero; a dashboard would read it as reviewed and clean")
	}
}

func TestReviewRateRefusesAnUnknownRepoName(t *testing.T) {
	// A typo'd -repo that printed an empty report would read as "this repo has
	// no findings" — a different and much more comforting claim.
	err := doReviewRate(rosterFor(t, ledgerRepo(t).Path), "typo", false)
	if err == nil {
		t.Fatal("a -repo matching nothing printed an empty report instead of failing")
	}
	if !strings.Contains(err.Error(), "typo") {
		t.Errorf("refusal does not name the repo asked for: %v", err)
	}
}

func TestReviewRateReadsARealLedger(t *testing.T) {
	// The happy path end to end: roster in, counts out, no error.
	repo := ledgerRepo(t, finding("accepted"), finding("rejected"), finding("ignored"))
	if err := doReviewRate(rosterFor(t, repo.Path), "r", false); err != nil {
		t.Errorf("review-rate failed on a healthy ledger: %v", err)
	}
}

func TestRateStringReportsMeasuredPrecision(t *testing.T) {
	r := rate(dispositions(t, map[string]int{"accepted": 8, "rejected": 2, "ignored": 5}))
	r.Repo = "agentic-flywheel"
	got := r.String()
	for _, want := range []string{"80%", "8 accepted", "2 rejected", "5 ignored", "10 judged"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want it to contain %q", got, want)
		}
	}
}

func TestRateStringNamesUnbucketedFindings(t *testing.T) {
	// The gap between Total and the three buckets must be visible, or the
	// counts silently fail to add up.
	r := rate(dispositions(t, map[string]int{"accepted": 1, "reviewed-later": 2}))
	r.Repo = "r"
	if !strings.Contains(r.String(), "2 with no recognised disposition") {
		t.Errorf("String() = %q, want it to name the 2 unbucketed findings", r.String())
	}
}

func TestTheNumberIsNotCalledPrecision(t *testing.T) {
	// The ledger does not record who judged a finding separately from who
	// raised it, and most entries are the same builder in the same run. The
	// figure is real; calling it "precision" is what makes it misleading —
	// the same shape as the 100% gate rate and the zero rework that both had
	// to be retracted (fw-bu2). Guard the label, not just the arithmetic.
	r := Rate{Repo: "r", Total: 12, Accepted: 8, Rejected: 3, Ignored: 1,
		Measurable: true, Precision: 8.0 / 11.0}
	got := r.String()
	if strings.Contains(got, "precision 73%") || !strings.Contains(got, "self-agreement") {
		t.Errorf("rendered as reviewer precision:\n  %s", got)
	}
	if !strings.Contains(got, "fw-bu2") {
		t.Errorf("does not point at what would make it precision:\n  %s", got)
	}
}
