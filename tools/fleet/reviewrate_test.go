package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// finding builds one ledger line with the given disposition and no attribution
// at all — the shape of every line written before guard.sh stamped who raised
// and who judged. The other fields are fixed because none of them affect the
// arithmetic under test.
func finding(disposition string) string {
	return judgedFinding(disposition, "", "")
}

// judgedFinding is the same line carrying attribution. An empty agent or
// judgedBy is omitted rather than written as "", so the fixture matches what a
// ledger of that vintage actually holds instead of a hand-made approximation.
func judgedFinding(disposition, agent, judgedBy string) string {
	line := `{"ts":"2026-08-17T01:32:34Z","commit":"6c77519","branch":"bead/fw-x","repo":"r",`
	if agent != "" {
		line += `"agent":"` + agent + `",`
	}
	if judgedBy != "" {
		line += `"judged_by":"` + judgedBy + `",`
	}
	return line + `"lens":"correctness","file":"a.go","line":"1","severity":"medium",` +
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

// readRepoLedger is the same read for a ledger built line by line, when the
// test cares about attribution rather than disposition counts.
func readRepoLedger(t *testing.T, lines ...string) []ReviewFinding {
	t.Helper()
	repo := ledgerRepo(t, lines...)
	fs, err := readLedger(filepath.Join(repo.Path, ".flywheel", "review.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestTally(t *testing.T) {
	tests := []struct {
		name   string
		counts map[string]int
		want   Tally
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
			want:    Tally{Ignored: 20, Total: 20},
			wantWhy: "none accepted or rejected",
		},
		{
			// The mirror image: an all-accepted ledger below the sample floor
			// must not report 100% either. Flattering the reviewer is the same
			// error as maligning it.
			name:    "all accepted below the floor is unmeasurable, not one hundred percent",
			counts:  map[string]int{"accepted": 4},
			want:    Tally{Accepted: 4, Total: 4},
			wantWhy: "only 4 of 4 finding(s) judged",
		},
		{
			// Rejected and ignored are distinct buckets. If they were summed,
			// this would be 6 of one and none of the other.
			name:   "rejected and ignored stay distinct",
			counts: map[string]int{"rejected": 2, "ignored": 4},
			want:   Tally{Rejected: 2, Ignored: 4, Total: 6},
		},
		{
			// Agreement divides by judged findings only. Were the 90 ignored
			// in the denominator this would be 8%, not 80%.
			name:   "agreement ignores the ignored",
			counts: map[string]int{"accepted": 8, "rejected": 2, "ignored": 90},
			want: Tally{
				Accepted: 8, Rejected: 2, Ignored: 90, Total: 100,
				Agreement: 0.8, Measurable: true,
			},
		},
		{
			name:   "exactly at the floor measures",
			counts: map[string]int{"accepted": 5, "rejected": 5},
			want: Tally{
				Accepted: 5, Rejected: 5, Total: 10,
				Agreement: 0.5, Measurable: true,
			},
		},
		{
			name:    "one short of the floor does not",
			counts:  map[string]int{"accepted": 5, "rejected": 4},
			want:    Tally{Accepted: 5, Rejected: 4, Total: 9},
			wantWhy: "only 9 of 9 finding(s) judged",
		},
		{
			// An unrecognised disposition is counted in Total and in no
			// bucket. Folding it into "ignored" would be the same mistake as
			// folding "ignored" into "rejected", one layer down.
			name:    "unknown dispositions land in no bucket",
			counts:  map[string]int{"accepted": 1, "reviewed-later": 2, "": 1},
			want:    Tally{Accepted: 1, Total: 4},
			wantWhy: "only 1 of 4 finding(s) judged",
		},
		{
			name:    "an empty ledger has nothing to say",
			counts:  nil,
			want:    Tally{},
			wantWhy: "none recorded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rate(dispositions(t, tt.counts))

			counts := got.All
			counts.Why = "" // compared separately
			if counts != tt.want {
				t.Errorf("All = %+v, want %+v", counts, tt.want)
			}
			if tt.wantWhy != "" && !strings.Contains(got.All.Why, tt.wantWhy) {
				t.Errorf("Why = %q, want it to mention %q", got.All.Why, tt.wantWhy)
			}
			// These fixtures carry no attribution, so the whole ledger must
			// land in Unrecorded — never in Independent, whatever the counts.
			if got.Unrecorded != got.All {
				t.Errorf("unattributed findings did not all land in Unrecorded:\n  %+v\n  %+v", got.Unrecorded, got.All)
			}
			if got.Independent.Total != 0 {
				t.Errorf("Independent = %+v on a ledger that names no judge", got.Independent)
			}
			// No unmeasurable tally may ever render a percentage. A "0%" or
			// "100%" here is a manufactured number, which is the whole thing
			// this command exists not to do.
			for _, ta := range []Tally{got.All, got.Independent, got.Self, got.Unrecorded} {
				if !ta.Measurable && strings.Contains(ta.line("x"), "%") {
					t.Errorf("unmeasurable tally rendered a percentage: %q", ta.line("x"))
				}
				if !ta.Measurable && ta.Agreement != 0 {
					t.Errorf("Agreement = %v on an unmeasurable tally; a caller may print it", ta.Agreement)
				}
			}
		})
	}
}

// The bead's own question: who judged this, and was it the same agent that
// raised it? Absence on either side is "cannot tell", never "independent".
func TestClassifiesByWhoJudged(t *testing.T) {
	tests := []struct {
		name            string
		agent, judgedBy string
		want            judgeClass
	}{
		{"a line written before attribution existed", "", "", unrecorded},
		{"raised by a named agent, judged by nobody recorded", "r/builder", "", unrecorded},
		{"judged by a named agent, raised by nobody recorded", "", "james", unrecorded},
		{"the same agent on both sides", "r/builder", "r/builder", selfJudged},
		{"two different agents", "r/builder", "james", independent},
		// guard.sh writes the literal "unknown" when it cannot resolve an
		// agent. Treating it as a name would make an unattributed finding
		// judged by a real person look independent, and two anonymous
		// findings look like the same agent.
		{"unknown against a real name is not independence", "unknown", "james", unrecorded},
		{"unknown on both sides is not the same agent", "unknown", "unknown", unrecorded},
		{"whitespace is not an attribution", "   ", "james", unrecorded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := ReviewFinding{Agent: tt.agent, JudgedBy: tt.judgedBy}
			if got := f.class(); got != tt.want {
				t.Errorf("class(agent=%q, judged_by=%q) = %d, want %d", tt.agent, tt.judgedBy, got, tt.want)
			}
		})
	}
}

// The acceptance criterion, stated as a test: a ledger written before the
// distinction existed must not be silently counted as independent. Big enough
// to clear the sample floor, so the only thing keeping it out of the precision
// figure is the missing attribution.
func TestAPreAttributionLedgerIsNeverCountedAsIndependent(t *testing.T) {
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, finding("accepted"))
	}
	for i := 0; i < 4; i++ {
		lines = append(lines, finding("rejected"))
	}
	r := rate(readRepoLedger(t, lines...))
	r.Repo = "r"

	if r.Independent.Total != 0 || r.Independent.Measurable {
		t.Errorf("Independent = %+v, want an empty unmeasurable tally", r.Independent)
	}
	if r.Unrecorded.Total != 14 || !r.Unrecorded.Measurable {
		t.Errorf("Unrecorded = %+v, want all 14 findings and a measurable agreement", r.Unrecorded)
	}
	// The old number is not deleted — it is a true account of what was
	// recorded — it just stops being called precision.
	got := r.String()
	if !strings.Contains(got, "judge unrecorded") || !strings.Contains(got, "71%") {
		t.Errorf("the recorded agreement vanished instead of being renamed:\n%s", got)
	}
	if pct := strings.Contains(precisionLine(t, r), "%"); pct {
		t.Errorf("precision line carries a percentage on a ledger with no independent judgement:\n%s", got)
	}
}

// renderedLine returns the one rendered line carrying the given label, so a
// test asserts on the right row without depending on column padding. Matching
// the whole rendering instead is how a negative assertion ends up passing for
// the wrong reason: one space out and it can never fire.
func renderedLine(t *testing.T, r Rate, label string) string {
	t.Helper()
	for _, l := range strings.Split(r.String(), "\n") {
		if strings.Contains(l, label) {
			return l
		}
	}
	t.Fatalf("no %q line in:\n%s", label, r.String())
	return ""
}

func precisionLine(t *testing.T, r Rate) string {
	t.Helper()
	return renderedLine(t, r, "precision")
}

// Each class clears the sample floor on its own evidence. A reviewer that
// agreed with itself two hundred times must not thereby unlock a precision
// figure computed from three independent judgements.
func TestSelfJudgedFindingsDoNotUnlockPrecision(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, judgedFinding("accepted", "r/builder", "r/builder"))
	}
	for i := 0; i < 3; i++ {
		lines = append(lines, judgedFinding("accepted", "r/builder", "james"))
	}
	r := rate(readRepoLedger(t, lines...))
	r.Repo = "r"

	if !r.Self.Measurable || r.Self.Total != 20 {
		t.Errorf("Self = %+v, want 20 findings and a measurable agreement", r.Self)
	}
	if r.Independent.Total != 3 {
		t.Errorf("Independent.Total = %d, want 3", r.Independent.Total)
	}
	if r.Independent.Measurable {
		t.Errorf("3 independent judgements measured a precision: %+v", r.Independent)
	}
	if strings.Contains(precisionLine(t, r), "%") {
		t.Errorf("precision rendered a percentage below its own sample floor:\n%s", r.String())
	}
	if got := renderedLine(t, r, "self-agreement"); !strings.Contains(got, "100%") {
		t.Errorf("self-agreement was not reported separately: %q", got)
	}
}

// Independent judgements, once there are enough of them, are precision and are
// named as such — the other half of the bead. Without this the fix would be
// indistinguishable from never reporting a precision at all.
func TestIndependentJudgementsAreReportedAsPrecision(t *testing.T) {
	var lines []string
	for i := 0; i < 9; i++ {
		lines = append(lines, judgedFinding("accepted", "r/builder", "james"))
	}
	for i := 0; i < 3; i++ {
		lines = append(lines, judgedFinding("rejected", "r/builder", "james"))
	}
	lines = append(lines, judgedFinding("accepted", "r/builder", "r/builder"))
	r := rate(readRepoLedger(t, lines...))
	r.Repo = "r"

	if !r.Independent.Measurable || r.Independent.Total != 12 {
		t.Fatalf("Independent = %+v, want 12 findings and a measurable precision", r.Independent)
	}
	if got := precisionLine(t, r); !strings.Contains(got, "75%") || !strings.Contains(got, "12 judged") {
		t.Errorf("precision line = %q, want 75%% over 12 judged", got)
	}
	// The self-judged finding must not be in the precision denominator: 10 of
	// 13 would be 77%, and the difference is the whole point.
	if strings.Contains(r.String(), "77%") {
		t.Errorf("a self-judged finding leaked into precision:\n%s", r.String())
	}
}

// The classes partition the ledger. If they did not, the report would either
// double-count a finding or lose one, and the counts on screen would not add
// up to the total beside them.
func TestClassesPartitionTheLedger(t *testing.T) {
	r := rate(readRepoLedger(t,
		finding("accepted"),
		judgedFinding("rejected", "r/builder", "r/builder"),
		judgedFinding("ignored", "r/builder", "james"),
		judgedFinding("accepted", "unknown", "james"),
		judgedFinding("accepted", "r/builder", ""),
	))
	if sum := r.Independent.Total + r.Self.Total + r.Unrecorded.Total; sum != r.All.Total {
		t.Errorf("classes total %d, ledger holds %d", sum, r.All.Total)
	}
	if r.All.Total != 5 {
		t.Errorf("All.Total = %d, want 5", r.All.Total)
	}
	if r.Independent.Total != 1 || r.Self.Total != 1 || r.Unrecorded.Total != 3 {
		t.Errorf("independent/self/unrecorded = %d/%d/%d, want 1/1/3",
			r.Independent.Total, r.Self.Total, r.Unrecorded.Total)
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
	r.Repo = "r"
	if r.All.Measurable || r.All.Total != 0 {
		t.Errorf("rate = %+v, want an empty unmeasurable rate", r.All)
	}
	if !strings.Contains(r.String(), "no reviews recorded") {
		t.Errorf("String() = %q, want it to say no reviews were recorded", r.String())
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
	fs := readRepoLedger(t,
		finding("accepted"),
		"",
		`{"ts":"2026-08-17T01:32:3`,
		"   ",
		finding("rejected"),
	)
	if len(fs) != 2 {
		t.Fatalf("read %d finding(s), want 2 — a corrupt or blank line lost real entries", len(fs))
	}
	if fs[0].Disposition != "accepted" || fs[1].Disposition != "rejected" {
		t.Errorf("dispositions = %q/%q, want accepted/rejected", fs[0].Disposition, fs[1].Disposition)
	}
}

func TestReadLedgerParsesEveryField(t *testing.T) {
	// The struct is the ledger's schema. A field that silently stops parsing
	// would drop evidence without anyone noticing — and a judged_by that did
	// so would quietly demote an independent judgement to unrecorded.
	fs := readRepoLedger(t, judgedFinding("accepted", "r/builder", "james"))
	if len(fs) != 1 {
		t.Fatalf("read %d finding(s), want 1", len(fs))
	}
	want := ReviewFinding{
		TS: "2026-08-17T01:32:34Z", Commit: "6c77519", Branch: "bead/fw-x", Repo: "r",
		Agent: "r/builder", JudgedBy: "james",
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
	if err := doReviewRate(rosterFor(t, repo.Path), "r", true); err != nil {
		t.Errorf("review-rate -json failed on a healthy ledger: %v", err)
	}
}

// A dashboard reading -json must not find a top-level "precision" key. The
// text output already carried the caveat; the JSON did not, so a consumer got
// the flattering number with nothing attached to it. The key is removed rather
// than renamed so such a consumer breaks loudly instead of quietly rescaling a
// number whose meaning changed (fw-bu2).
func TestNoJSONKeyIsCalledPrecision(t *testing.T) {
	for _, f := range []string{"reviewrate.go", "main.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `json:"precision"`) {
			t.Errorf(`%s marshals a field as "precision"; only .independent.agreement is one`, f)
		}
	}
}

func TestTallyStringReportsMeasuredAgreement(t *testing.T) {
	r := rate(dispositions(t, map[string]int{"accepted": 8, "rejected": 2, "ignored": 5}))
	got := r.Unrecorded.line("judge unrecorded")
	for _, want := range []string{"80%", "8 accepted", "2 rejected", "5 ignored", "10 judged"} {
		if !strings.Contains(got, want) {
			t.Errorf("line() = %q, want it to contain %q", got, want)
		}
	}
}

func TestTallyStringNamesUnbucketedFindings(t *testing.T) {
	// The gap between Total and the three buckets must be visible, or the
	// counts silently fail to add up.
	r := rate(dispositions(t, map[string]int{"accepted": 1, "reviewed-later": 2}))
	r.Repo = "r"
	if !strings.Contains(r.String(), "2 with no recognised disposition") {
		t.Errorf("String() = %q, want it to name the 2 unbucketed findings", r.String())
	}
}

func TestTheNumberIsNotCalledPrecision(t *testing.T) {
	// The ledger did not record who judged a finding separately from who
	// raised it, and most entries are the same builder in the same run. The
	// figure is real; calling it "precision" is what makes it misleading —
	// the same shape as the 100% gate rate and the zero rework that both had
	// to be retracted (fw-bu2). Guard the label, not just the arithmetic.
	r := rate(readRepoLedger(t, func() []string {
		var l []string
		for i := 0; i < 8; i++ {
			l = append(l, finding("accepted"))
		}
		for i := 0; i < 3; i++ {
			l = append(l, finding("rejected"))
		}
		return append(l, finding("ignored"))
	}()...))
	r.Repo = "r"

	if got := precisionLine(t, r); strings.Contains(got, "%") {
		t.Errorf("rendered the recorded agreement as reviewer precision: %q", got)
	}
	if got := renderedLine(t, r, "judge unrecorded"); !strings.Contains(got, "73%") {
		t.Errorf("does not name the figure for what it is: %q", got)
	}
}
