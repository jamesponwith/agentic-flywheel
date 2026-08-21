// Review-rate accounting (fw-dov).
//
// WRITEUP.md names two things the flywheel cannot yet tell you about itself.
// One is whether the AI reviewer catches more than it costs — and half that
// answer was already on disk, unread. Every `guard.sh finding` appends a line
// to .flywheel/review.jsonl carrying a disposition; nothing but `wc -l` had
// ever looked at it.
//
// Two refusals carry the weight here, and both are about declining to
// manufacture a number that flatters the reviewer:
//
//   - "ignored" is not "rejected". A finding nobody judged is not a finding
//     judged wrong. Collapsing the two lets you pick whichever denominator
//     gives the prettier precision, so ignored never enters it at all.
//   - A handful of findings is not a rate. Below minSample this says so
//     rather than dividing, the same way cost.go distinguishes zero spend
//     from unmeasured spend.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// minSample is how many findings must have been *judged* — accepted or
// rejected — before a precision figure is worth printing. Ignored findings
// never count toward it: they are the ones nobody looked at, and letting them
// unlock a rate would mean a reviewer could reach significance by being
// steadily disregarded.
const minSample = 10

// ReviewFinding is one line of the review ledger, exactly as `guard.sh
// finding` writes it. Every field is a string because the writer is shell and
// quotes everything, Line included.
//
// Named for its ledger rather than just "Finding": doctor.go already owns that
// name for a missing artifact, and the two are unrelated.
type ReviewFinding struct {
	TS          string `json:"ts"`
	Commit      string `json:"commit"`
	Branch      string `json:"branch"`
	Repo        string `json:"repo"`
	Lens        string `json:"lens"`
	File        string `json:"file"`
	Line        string `json:"line"`
	Severity    string `json:"severity"`
	Claim       string `json:"claim"`
	Disposition string `json:"disposition"`
}

// Rate is what one repo's ledger says about its reviewer.
type Rate struct {
	Repo     string `json:"repo"`
	Accepted int    `json:"accepted"`
	Rejected int    `json:"rejected"`
	Ignored  int    `json:"ignored"`
	Total    int    `json:"total"`
	// Precision is Accepted/(Accepted+Rejected) — findings judged and agreed
	// with, over findings judged at all. NOTE: the ledger does not record who
	// judged separately from who raised, so today this is self-agreement and
	// is rendered under that name (fw-bu2). Meaningless unless
	// Measurable, and left at zero in that case so a careless reader cannot
	// mistake it for a real zero.
	Precision float64 `json:"precision"`
	// Measurable is false when too few findings have been judged to divide.
	// Why names which of the three reasons applies.
	Measurable bool   `json:"measurable"`
	Why        string `json:"why,omitempty"`
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

// rate buckets findings by disposition and divides only when it is honest to.
//
// Dispositions outside the three known values are counted in Total and in no
// bucket. That gap is deliberate: silently folding an unrecognised disposition
// into "ignored" would be the same error as folding "ignored" into "rejected",
// one layer down.
func rate(fs []ReviewFinding) Rate {
	r := Rate{Total: len(fs)}
	for _, f := range fs {
		switch f.Disposition {
		case "accepted":
			r.Accepted++
		case "rejected":
			r.Rejected++
		case "ignored":
			r.Ignored++
		}
	}
	judged := r.Accepted + r.Rejected
	switch {
	case r.Total == 0:
		r.Why = "no reviews recorded"
	case judged == 0:
		r.Why = fmt.Sprintf("%d finding(s) recorded, none accepted or rejected — "+
			"an unjudged finding is not a wrong one, so there is no rate to report", r.Total)
	case judged < minSample:
		r.Why = fmt.Sprintf("only %d of %d finding(s) judged, %d needed before precision means anything",
			judged, r.Total, minSample)
	default:
		r.Measurable = true
		r.Precision = float64(r.Accepted) / float64(judged)
	}
	return r
}

func (r Rate) String() string {
	if r.Total == 0 {
		return fmt.Sprintf("  %-22s %s", r.Repo, r.Why)
	}
	line := fmt.Sprintf("  %-22s %d finding(s): %d accepted, %d rejected, %d ignored",
		r.Repo, r.Total, r.Accepted, r.Rejected, r.Ignored)
	if n := r.Total - r.Accepted - r.Rejected - r.Ignored; n > 0 {
		line += fmt.Sprintf(", %d with no recognised disposition", n)
	}
	if !r.Measurable {
		// No percentage anywhere on this path: an unmeasurable rate that
		// renders as "0%" is exactly the false precision this command exists
		// to refuse.
		return line + " — " + r.Why
	}
	// "self-agreement", not "precision", until the ledger records who judged a
	// finding separately from who raised it. Today most entries are the same
	// builder in the same run, so the figure is a reviewer agreeing with
	// itself. That is not wrong, it is flattering in a way no reader would
	// guess — the same shape as the 100% gate rate and the zero rework that
	// both had to be retracted. It gets the honest name until fw-bu2 adds
	// judged_by, and then it can earn the other one.
	return line + fmt.Sprintf(" — %.0f%% self-agreement over %d judged (not reviewer precision: fw-bu2)",
		r.Precision*100, r.Accepted+r.Rejected)
}
