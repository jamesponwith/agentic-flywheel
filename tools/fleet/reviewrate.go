// Review-rate accounting (fw-dov, fw-bu2).
//
// WRITEUP.md names two things the flywheel cannot yet tell you about itself.
// One is whether the AI reviewer catches more than it costs — and half that
// answer was already on disk, unread. Every `guard.sh finding` appends a line
// to .flywheel/review.jsonl carrying a disposition; nothing but `wc -l` had
// ever looked at it.
//
// Three refusals carry the weight here, and all three are about declining to
// manufacture a number that flatters the reviewer:
//
//   - "ignored" is not "rejected". A finding nobody judged is not a finding
//     judged wrong. Collapsing the two lets you pick whichever denominator
//     gives the prettier precision, so ignored never enters it at all.
//   - A handful of findings is not a rate. Below minSample this says so
//     rather than dividing, the same way cost.go distinguishes zero spend
//     from unmeasured spend.
//   - A reviewer agreeing with itself is not precision. Under ADR 0013 the
//     panel runs inside the builder's own loop, so almost every finding is
//     raised and dispositioned by one agent in one run. That figure is real
//     and it is not what "precision" means to anyone reading a dashboard, so
//     the two are counted apart and only the independent one gets the word.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// minSample is how many findings must have been *judged* — accepted or
// rejected — before a rate is worth printing. Ignored findings never count
// toward it: they are the ones nobody looked at, and letting them unlock a
// rate would mean a reviewer could reach significance by being steadily
// disregarded. It applies per class, so an independent precision needs
// minSample independent judgements and cannot borrow the self-judged ones.
const minSample = 10

// ReviewFinding is one line of the review ledger, exactly as `guard.sh
// finding` writes it. Every field is a string because the writer is shell and
// quotes everything, Line included.
//
// Named for its ledger rather than just "Finding": doctor.go already owns that
// name for a missing artifact, and the two are unrelated.
type ReviewFinding struct {
	TS     string `json:"ts"`
	Commit string `json:"commit"`
	Branch string `json:"branch"`
	Repo   string `json:"repo"`
	// Agent is who raised the finding; JudgedBy is who dispositioned it. They
	// are separate because they are usually the same agent and nobody could
	// tell (fw-bu2). Both may be empty: every line written before the
	// distinction existed has neither, which is why absence has to mean
	// "unknown" rather than "the same" or "different".
	Agent       string `json:"agent"`
	JudgedBy    string `json:"judged_by"`
	Lens        string `json:"lens"`
	File        string `json:"file"`
	Line        string `json:"line"`
	Severity    string `json:"severity"`
	Claim       string `json:"claim"`
	Disposition string `json:"disposition"`
}

// judgeClass says whether a finding's disposition can be *shown* to have come
// from someone other than whoever raised it.
type judgeClass int

const (
	// unrecorded is deliberately the zero value: a finding this code failed to
	// classify must land in the bucket that proves nothing, never in the one
	// that would flatter the reviewer.
	unrecorded judgeClass = iota
	selfJudged
	independent
)

// attributed normalises an attribution to "" when it names nobody. guard.sh
// writes the literal "unknown" when FLYWHEEL_AGENT is unset and no
// .flywheel/agent file exists, and "unknown" is not a name: two findings both
// stamped "unknown" are not evidence of the same agent, and one stamped
// "unknown" against a named judge is not evidence of a different one.
func attributed(s string) string {
	if s = strings.TrimSpace(s); s == "unknown" {
		return ""
	}
	return s
}

func (f ReviewFinding) class() judgeClass {
	raised, judged := attributed(f.Agent), attributed(f.JudgedBy)
	switch {
	case raised == "" || judged == "":
		return unrecorded
	case raised == judged:
		return selfJudged
	default:
		return independent
	}
}

// Tally is the arithmetic over one set of findings, and the decision about
// whether it is honest to divide them.
//
// The number is called Agreement, never Precision, and no method here renders
// a label. Only the caller knows which set it is holding, so only the caller
// can name it — which is the point: the arithmetic cannot smuggle the word
// "precision" onto a figure that has not earned it.
type Tally struct {
	Accepted   int     `json:"accepted"`
	Rejected   int     `json:"rejected"`
	Ignored    int     `json:"ignored"`
	Total      int     `json:"total"`
	Agreement  float64 `json:"agreement"`
	Measurable bool    `json:"measurable"`
	Why        string  `json:"why,omitempty"`
}

// Rate is what one repo's ledger says about its reviewer.
//
// All is every finding however attributed, and the three classes partition it
// exactly. There is no top-level agreement figure: a single number over the
// whole ledger is the one this bead exists to stop printing.
type Rate struct {
	Repo string `json:"repo"`
	All  Tally  `json:"all"`
	// Independent is the only one of the three that is reviewer precision.
	Independent Tally `json:"independent"`
	Self        Tally `json:"self_judged"`
	Unrecorded  Tally `json:"judge_unrecorded"`
}

// readLedger parses .flywheel/review.jsonl: one JSON object per line, tolerant
// of trailing and blank lines.
//
// A ledger that does not exist is not an error — it is a repo nobody has
// reviewed yet, which is a fact worth reporting rather than a failure. A
// ledger that exists and cannot be read IS an error, because that is a broken
// thing pretending to be an empty one.
func readLedger(path string) ([]ReviewFinding, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []ReviewFinding
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20) // claims run long
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var fd ReviewFinding
		// ponytail: a malformed line is skipped, not counted, matching
		// ReadSpend — one bad line must not lose the whole ledger. guard.sh's
		// json_pairs is currently the only writer and always quotes its
		// values, so a torn append from two concurrent builders is the only
		// realistic source. If a second writer ever appears, return the skip
		// count so the caller can say how much it could not read.
		if json.Unmarshal(line, &fd) != nil {
			continue
		}
		out = append(out, fd)
	}
	return out, sc.Err()
}

// tally buckets findings by disposition and divides only when it is honest to.
//
// Dispositions outside the three known values are counted in Total and in no
// bucket. That gap is deliberate: silently folding an unrecognised disposition
// into "ignored" would be the same error as folding "ignored" into "rejected",
// one layer down.
func tally(fs []ReviewFinding) Tally {
	t := Tally{Total: len(fs)}
	for _, f := range fs {
		switch f.Disposition {
		case "accepted":
			t.Accepted++
		case "rejected":
			t.Rejected++
		case "ignored":
			t.Ignored++
		}
	}
	judged := t.Accepted + t.Rejected
	switch {
	case t.Total == 0:
		t.Why = "none recorded"
	case judged == 0:
		t.Why = fmt.Sprintf("%d finding(s) recorded, none accepted or rejected — "+
			"an unjudged finding is not a wrong one, so there is no rate to report", t.Total)
	case judged < minSample:
		t.Why = fmt.Sprintf("only %d of %d finding(s) judged, %d needed before a rate means anything",
			judged, t.Total, minSample)
	default:
		t.Measurable = true
		t.Agreement = float64(t.Accepted) / float64(judged)
	}
	return t
}

// rate splits a ledger by who judged what, then runs the same arithmetic over
// each class. Findings that name neither party stay in Unrecorded, where they
// prove nothing — a ledger written before guard.sh stamped attribution must
// not be counted as independent by default (fw-bu2).
func rate(fs []ReviewFinding) Rate {
	var byClass [3][]ReviewFinding
	for _, f := range fs {
		c := f.class()
		byClass[c] = append(byClass[c], f)
	}
	return Rate{
		All:         tally(fs),
		Independent: tally(byClass[independent]),
		Self:        tally(byClass[selfJudged]),
		Unrecorded:  tally(byClass[unrecorded]),
	}
}

// line renders one class under the name that class has earned. label is the
// caller's word, not the Tally's: see the type comment.
func (t Tally) line(label string) string {
	body := fmt.Sprintf("%d finding(s): %d accepted, %d rejected, %d ignored",
		t.Total, t.Accepted, t.Rejected, t.Ignored)
	if n := t.Total - t.Accepted - t.Rejected - t.Ignored; n > 0 {
		body += fmt.Sprintf(", %d with no recognised disposition", n)
	}
	switch {
	case t.Total == 0:
		return fmt.Sprintf("      %-17s %s", label, t.Why)
	case !t.Measurable:
		// No percentage anywhere on this path: an unmeasurable rate that
		// renders as "0%" is exactly the false precision this command exists
		// to refuse.
		return fmt.Sprintf("      %-17s %s — %s", label, body, t.Why)
	default:
		return fmt.Sprintf("      %-17s %.0f%% over %d judged (%s)",
			label, t.Agreement*100, t.Accepted+t.Rejected, body)
	}
}

func (r Rate) String() string {
	if r.All.Total == 0 {
		return fmt.Sprintf("  %-22s %s", r.Repo, "no reviews recorded")
	}
	return strings.Join([]string{
		fmt.Sprintf("  %-22s %d finding(s)", r.Repo, r.All.Total),
		// "precision" appears on exactly one of these lines, and that line
		// prints a percentage only when independent judgements cleared the
		// sample floor. Everything else gets the honest, duller name.
		r.Independent.line("precision"),
		r.Self.line("self-agreement"),
		r.Unrecorded.line("judge unrecorded"),
	}, "\n")
}
