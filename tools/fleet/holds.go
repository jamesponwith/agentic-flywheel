package main

import (
	"fmt"
	"os"
	"time"
)

// earliestHold is the soonest future quota hold across the whole roster.
//
// noteRateLimit writes the hold into the repo whose builder hit the wall, so a
// Spotify builder's 429 lands in Spotify's .flywheel/quota-hold. nightly.sh
// read only its own repo's file, found a two-day-old expired hold, computed a
// negative delay and scheduled nothing — the fleet then sat until a person
// noticed, which is the outage the hold exists to prevent, reached by a
// different route.
//
// Earliest wins because the account is one pool: the first reset is when work
// can start again wherever it was blocked.
func earliestHold(r Roster, now time.Time) (time.Time, string, bool) {
	var best time.Time
	var why string
	for _, repo := range r.Repos {
		until, reason, held := heldUntil(repo.Path, now)
		if !held {
			continue
		}
		if best.IsZero() || until.Before(best) {
			best, why = until, reason
		}
	}
	return best, why, !best.IsZero()
}

// cmdHolds prints the earliest future hold as an RFC3339 timestamp, or nothing
// at all when the fleet is free to run. Printing nothing rather than a zero
// time keeps the shell test a plain emptiness check.
func cmdHolds(r Roster) error {
	until, why, held := earliestHold(r, time.Now())
	if !held {
		return nil
	}
	fmt.Println(until.UTC().Format(time.RFC3339))
	if why != "" {
		fmt.Fprintln(os.Stderr, why)
	}
	return nil
}
