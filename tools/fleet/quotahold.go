// Pacing the fleet against the account's real limit.
//
// The first version rationed dollars, which a Max subscription does not charge
// — total_cost_usd is API-equivalent valuation, not money. The second rationed
// dollars per window, which was the right shape in the wrong unit: it still
// required someone to guess a number that maps to nothing the account
// publishes.
//
// The account is the authority on its own limits, and it says so out loud. A
// 429 carries the moment it resets ("You've hit your session limit · resets 6pm
// (America/Los_Angeles)"). There is no CLI that reports remaining quota, so
// there is nothing to pace against in advance — but there is something exact to
// wait for afterwards. Run until the account says stop, wait precisely as long
// as it asks, continue. That uses the whole limit without a person in the loop,
// which is the point: a wall that needs a human to clear it is an outage.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// holdFile records when the account said its quota returns.
const holdFile = "quota-hold"

func holdPath(repoPath string) string {
	return filepath.Join(repoPath, ".flywheel", holdFile)
}

// resetPattern pulls the time out of the account's own message. Deliberately
// narrow: it matches what the runner actually emits, and anything it does not
// recognise falls back to a conservative wait rather than to a guess that could
// resume early and burn a builder's startup against a wall that is still there.
var resetPattern = regexp.MustCompile(`(?i)resets?\s+(\d{1,2})(?::(\d{2}))?\s*([ap]m)?(?:\s*\(([^)]+)\))?`)

// defaultHold is how long to wait when the message carries no readable time.
// An hour, not a day: too short wastes one builder's startup discovering the
// wall again, too long idles the account through a window it had already given
// back. Wasting a startup is the cheaper mistake.
const defaultHold = time.Hour

// parseReset reads the reset moment out of a rate-limit message.
//
// Returns the next occurrence of that clock time, in the timezone the message
// names when it names one. "resets 6pm" seen at 3pm means today at 18:00; seen
// at 7pm it means tomorrow, because the account does not say which day and the
// only reading that cannot resume early is the later one.
func parseReset(msg string, now time.Time) (time.Time, bool) {
	m := resetPattern.FindStringSubmatch(msg)
	if m == nil {
		return time.Time{}, false
	}
	hour, err := strconv.Atoi(m[1])
	if err != nil || hour > 23 {
		return time.Time{}, false
	}
	min := 0
	if m[2] != "" {
		if min, err = strconv.Atoi(m[2]); err != nil || min > 59 {
			return time.Time{}, false
		}
	}
	switch strings.ToLower(m[3]) {
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	}
	loc := now.Location()
	if m[4] != "" {
		if l, err := time.LoadLocation(strings.TrimSpace(m[4])); err == nil {
			loc = l
		}
		// An unknown zone falls through to local rather than failing: a hold in
		// the wrong zone is at worst a few hours of idling, while no hold at all
		// dispatches straight back into the wall.
	}
	local := now.In(loc)
	reset := time.Date(local.Year(), local.Month(), local.Day(), hour, min, 0, 0, loc)
	if !reset.After(local) {
		reset = reset.AddDate(0, 0, 1)
	}
	return reset, true
}

// holdUntil records that the fleet must not dispatch before t.
//
// Written to the repo rather than to memory because the process that learned
// about the wall exits, and the next scheduled run is a different process. A
// hold that lived only in memory would be forgotten by exactly the run it
// exists to stop.
func holdUntil(repoPath string, t time.Time, why string) error {
	p := holdPath(repoPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("%s\n%s\n", t.UTC().Format(time.RFC3339), why)
	return os.WriteFile(p, []byte(body), 0o644)
}

// heldUntil reports the active hold, if any. A hold whose time has passed is
// not a hold: the quota came back, and nothing needs to clear the file for the
// fleet to resume.
func heldUntil(repoPath string, now time.Time) (time.Time, string, bool) {
	b, err := os.ReadFile(holdPath(repoPath))
	if err != nil {
		return time.Time{}, "", false
	}
	lines := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(lines[0]))
	if err != nil {
		// A corrupt hold must not wedge the fleet forever, and must not be
		// ignored either. Treat it as a short hold and let it expire.
		return now.Add(defaultHold), "unreadable quota hold, waiting " + defaultHold.String(), true
	}
	if !t.After(now) {
		return time.Time{}, "", false
	}
	why := ""
	if len(lines) > 1 {
		why = strings.TrimSpace(lines[1])
	}
	return t, why, true
}

// noteRateLimit records a wall the runner just hit, using the account's own
// stated reset when it gave one.
func noteRateLimit(repoPath, message string, now time.Time) (time.Time, error) {
	reset, ok := parseReset(message, now)
	if !ok {
		reset = now.Add(defaultHold)
	}
	return reset, holdUntil(repoPath, reset, message)
}
