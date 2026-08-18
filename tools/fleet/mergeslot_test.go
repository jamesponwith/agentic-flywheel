package main

import (
	"strings"
	"testing"
	"time"
)

// bd reports contention on stderr. A stdout-only capture never saw it, so the
// "held" guard was dead code and any bd failure spun the full deadline.
func TestAcquireDoesNotSpinOnAFailingBd(t *testing.T) {
	calls := 0
	m := mergeSlot{bd: bdClient{dir: ".", run: func(string, ...string) ([]byte, error) {
		calls++
		return nil, errNotFound{"bd exploded"}
	}}}
	now := time.Now()
	clock := func() time.Time { now = now.Add(3 * time.Second); return now }

	ok, why := m.acquire(4*time.Second, clock)
	if ok {
		t.Fatal("claimed the slot from a failing bd")
	}
	if !strings.Contains(why, "do not push") {
		t.Errorf("message does not tell the caller what to do: %q", why)
	}
	if calls > 4 {
		t.Errorf("spun %d times against a 4s deadline", calls)
	}
}

// The old message claimed "branch pushed by nobody" about a branch the agent
// had certainly already pushed — the slot was acquired after the push.
func TestAcquireMessageDoesNotClaimNothingWasPushed(t *testing.T) {
	m := mergeSlot{bd: bdClient{dir: ".", run: func(string, ...string) ([]byte, error) {
		return nil, errNotFound{"held"}
	}}}
	now := time.Now()
	_, why := m.acquire(time.Nanosecond, func() time.Time { now = now.Add(time.Second); return now })
	if strings.Contains(why, "pushed by nobody") {
		t.Error("message still asserts nothing was pushed")
	}
}
