package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// theRealMessage is what the account actually sent when it stopped the first
// unattended night. Every parsing decision below is answerable to this string.
const theRealMessage = "You've hit your session limit · resets 6pm (America/Los_Angeles)"

func TestParsesTheMessageTheAccountActuallySent(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	now := time.Date(2026, 8, 20, 15, 33, 0, 0, la) // when the builders died
	got, ok := parseReset(theRealMessage, now)
	if !ok {
		t.Fatal("could not read a reset time out of the account's own words")
	}
	want := time.Date(2026, 8, 20, 18, 0, 0, 0, la)
	if !got.Equal(want) {
		t.Errorf("reset = %s, want %s", got, want)
	}
}

func TestAnAmbiguousDayResolvesLate(t *testing.T) {
	// "resets 6pm" does not say which day. Read at 7pm it must mean tomorrow:
	// the earlier reading resumes into a wall that is still there and pays a
	// builder's startup to learn nothing.
	la, _ := time.LoadLocation("America/Los_Angeles")
	now := time.Date(2026, 8, 20, 19, 0, 0, 0, la)
	got, ok := parseReset(theRealMessage, now)
	if !ok {
		t.Fatal("did not parse")
	}
	if !got.After(now) {
		t.Errorf("reset %s is not in the future relative to %s", got, now)
	}
	if got.Day() != 21 {
		t.Errorf("resolved to day %d, want the 21st — an already-passed reset "+
			"must roll forward, never backward", got.Day())
	}
}

func TestUnreadableMessageStillHolds(t *testing.T) {
	// A message shape nobody anticipated must not mean "no limit". Failing open
	// here dispatches straight back into the wall, once per remaining bead.
	dir := t.TempDir()
	now := time.Now()
	reset, err := noteRateLimit(dir, "rate limited, try again sometime", now)
	if err != nil {
		t.Fatal(err)
	}
	if !reset.After(now) {
		t.Fatal("an unparseable limit produced no hold at all")
	}
	if _, _, held := heldUntil(dir, now); !held {
		t.Error("hold was not persisted; the next run walks into the same wall")
	}
}

func TestTheHoldExpiresWithoutAnyoneClearingIt(t *testing.T) {
	// The entire point: the quota comes back on its own, so the fleet must
	// resume on its own. A hold that needed a human to clear it would be the
	// menu-click this exists to remove.
	dir := t.TempDir()
	now := time.Now()
	if err := holdUntil(dir, now.Add(2*time.Hour), "session limit"); err != nil {
		t.Fatal(err)
	}
	if _, _, held := heldUntil(dir, now); !held {
		t.Fatal("not held during the window")
	}
	if _, _, held := heldUntil(dir, now.Add(3*time.Hour)); held {
		t.Error("still held after the account said the quota returned — the fleet " +
			"would idle until a person noticed and deleted a file")
	}
}

func TestACorruptHoldExpiresRatherThanWedging(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(holdPath(dir), []byte("not a timestamp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	until, _, held := heldUntil(dir, now)
	if !held {
		t.Error("a corrupt hold was read as no hold, which fails open into the wall")
	}
	if until.After(now.Add(2 * defaultHold)) {
		t.Errorf("corrupt hold parked the fleet until %s; it must expire, not wedge", until)
	}
}

func TestNoHoldMeansRun(t *testing.T) {
	if _, _, held := heldUntil(t.TempDir(), time.Now()); held {
		t.Error("held with no hold file; the fleet would never start")
	}
}
