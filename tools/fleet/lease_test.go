package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var base = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

// fakeBD is an in-memory bd. Every mutation is recorded so a test can assert
// what the fleet actually did, not just what it returned.
type fakeBD struct {
	beads map[string]*Bead
	order []string // insertion order: map iteration would make results flaky
	calls []string
	fail  map[string]bool
}

func newFake(bs ...Bead) *fakeBD {
	f := &fakeBD{beads: map[string]*Bead{}, fail: map[string]bool{}}
	for i := range bs {
		b := bs[i]
		if b.Metadata == nil {
			b.Metadata = map[string]string{}
		}
		f.beads[b.ID] = &b
		f.order = append(f.order, b.ID)
	}
	return f
}

func (f *fakeBD) run(dir string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "show":
		b, ok := f.beads[args[1]]
		if !ok {
			return nil, errNotFound{args[1]}
		}
		out, _ := json.Marshal(b)
		return out, nil
	case "list":
		var out []Bead
		for _, id := range f.order {
			if b := f.beads[id]; b.Status == args[2] {
				out = append(out, *b)
			}
		}
		j, _ := json.Marshal(out)
		return j, nil
	case "ready":
		var out []Bead
		for _, id := range f.order {
			if b := f.beads[id]; b.Status == "open" {
				out = append(out, *b)
			}
		}
		j, _ := json.Marshal(out)
		return j, nil
	case "update":
		b := f.beads[args[1]]
		switch args[2] {
		case "--claim":
			b.Status = "in_progress"
		case "--metadata":
			var md map[string]string
			_ = json.Unmarshal([]byte(args[3]), &md)
			b.Metadata = md
		case "--status":
			b.Status = args[3]
		}
		return []byte("{}"), nil
	case "note":
		return []byte("{}"), nil
	case "close":
		if f.fail["close "+args[1]] {
			return nil, errNotFound{args[1]}
		}
		b := f.beads[args[1]]
		b.Status = "closed"
		b.Metadata["close_reason"] = args[3]
		return []byte("{}"), nil
	}
	return []byte("{}"), nil
}

type errNotFound struct{ id string }

func (e errNotFound) Error() string { return e.id + ": not found" }

func newLeaser(f *fakeBD, now time.Time) leaser {
	return leaser{bd: bdClient{dir: ".", run: f.run}, now: func() time.Time { return now }}
}

func TestClaim(t *testing.T) {
	tests := []struct {
		name    string
		bead    Bead
		agent   string
		at      time.Time
		wantErr string
	}{
		{
			name:  "unheld bead is claimable",
			bead:  Bead{ID: "x-1", Status: "open"},
			agent: "router/builder", at: base,
		},
		{
			name: "live lease held by another agent is refused",
			bead: Bead{ID: "x-1", Status: "in_progress", Metadata: map[string]string{
				leaseHolderKey: "router/builder-a", leaseExpiresKey: base.Add(time.Hour).Format(time.RFC3339)}},
			agent: "router/builder-b", at: base,
			wantErr: "held by router/builder-a",
		},
		{
			name: "expired lease is claimable by anyone",
			bead: Bead{ID: "x-1", Status: "in_progress", Metadata: map[string]string{
				leaseHolderKey: "router/builder-a", leaseExpiresKey: base.Add(-time.Hour).Format(time.RFC3339)}},
			agent: "router/builder-b", at: base,
		},
		{
			name: "re-claiming your own live lease is fine",
			bead: Bead{ID: "x-1", Status: "in_progress", Metadata: map[string]string{
				leaseHolderKey: "router/builder", leaseExpiresKey: base.Add(time.Hour).Format(time.RFC3339)}},
			agent: "router/builder", at: base,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(tt.bead)
			err := newLeaser(f, tt.at).Claim(tt.bead.ID, tt.agent, time.Hour)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := f.beads[tt.bead.ID]
			if got.Status != "in_progress" {
				t.Errorf("status = %q, want in_progress", got.Status)
			}
			if got.Metadata[leaseHolderKey] != tt.agent {
				t.Errorf("holder = %q, want %q", got.Metadata[leaseHolderKey], tt.agent)
			}
			if got.Metadata[leaseExpiresKey] != tt.at.Add(time.Hour).UTC().Format(time.RFC3339) {
				t.Errorf("expires = %q", got.Metadata[leaseExpiresKey])
			}
		})
	}
}

func TestHeartbeatRefusesSomeoneElsesLease(t *testing.T) {
	// The reclaimed-agent-wakes-up case: it must not resurrect its lease and
	// start editing alongside whoever replaced it.
	f := newFake(Bead{ID: "x-1", Status: "in_progress", Metadata: map[string]string{
		leaseHolderKey: "router/builder-b", leaseExpiresKey: base.Add(time.Hour).Format(time.RFC3339)}})
	err := newLeaser(f, base).Heartbeat("x-1", "router/builder-a", time.Hour)
	if err == nil || !strings.Contains(err.Error(), "stop working and re-claim") {
		t.Fatalf("err = %v, want a refusal telling the stale agent to stop", err)
	}
	if f.beads["x-1"].Metadata[leaseHolderKey] != "router/builder-b" {
		t.Error("holder was overwritten by a stale agent")
	}
}

func TestHeartbeatExtends(t *testing.T) {
	f := newFake(Bead{ID: "x-1", Status: "in_progress", Metadata: map[string]string{
		leaseHolderKey: "a", leaseExpiresKey: base.Add(time.Minute).Format(time.RFC3339)}})
	if err := newLeaser(f, base).Heartbeat("x-1", "a", 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, want := f.beads["x-1"].Metadata[leaseExpiresKey], base.Add(2*time.Hour).Format(time.RFC3339); got != want {
		t.Errorf("expires = %q, want %q", got, want)
	}
}

func TestReleaseClearsLeaseAndRefusesOthers(t *testing.T) {
	f := newFake(Bead{ID: "x-1", Status: "in_progress", Metadata: map[string]string{
		leaseHolderKey: "a", leaseExpiresKey: base.Add(time.Hour).Format(time.RFC3339), "other": "keep"}})
	if err := newLeaser(f, base).Release("x-1", "b"); err == nil {
		t.Error("released another agent's lease")
	}
	if err := newLeaser(f, base).Release("x-1", "a"); err != nil {
		t.Fatal(err)
	}
	md := f.beads["x-1"].Metadata
	if _, ok := md[leaseHolderKey]; ok {
		t.Error("holder not cleared")
	}
	if md["other"] != "keep" {
		t.Error("release clobbered unrelated metadata")
	}
}

func TestReclaim(t *testing.T) {
	f := newFake(
		Bead{ID: "expired", Status: "in_progress", Metadata: map[string]string{
			leaseHolderKey: "dead/builder", leaseExpiresKey: base.Add(-30 * time.Minute).Format(time.RFC3339)}},
		Bead{ID: "live", Status: "in_progress", Metadata: map[string]string{
			leaseHolderKey: "alive/builder", leaseExpiresKey: base.Add(30 * time.Minute).Format(time.RFC3339)}},
		Bead{ID: "human", Status: "in_progress"}, // no lease: a person is working it
		Bead{ID: "open", Status: "open"},
	)
	got, err := newLeaser(f, base).Reclaim()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "expired" {
		t.Fatalf("reclaimed %+v, want only the expired one", got)
	}
	if got[0].Holder != "dead/builder" || got[0].Late != 30*time.Minute {
		t.Errorf("reclaimed = %+v, want holder dead/builder late 30m", got[0])
	}
	if f.beads["expired"].Status != "open" {
		t.Error("expired bead was not returned to the queue")
	}
	if _, ok := f.beads["expired"].Metadata[leaseHolderKey]; ok {
		t.Error("expired lease metadata not cleared")
	}
	if f.beads["live"].Status != "in_progress" {
		t.Error("a live lease was reclaimed")
	}
	if f.beads["human"].Status != "in_progress" {
		t.Error("reclaimed an unleased bead — that is a human's work")
	}
	var noted bool
	for _, c := range f.calls {
		if strings.HasPrefix(c, "note expired") && strings.Contains(c, "dead/builder") {
			noted = true
		}
	}
	if !noted {
		t.Error("no note naming who dropped the bead — the next agent needs to know about the stale worktree")
	}
}
