// Leases. `bd update --claim` is atomic but has no liveness: an agent that dies
// mid-bead leaves it claimed forever and the fleet quietly starves. A lease is
// a claim with an expiry the holder must keep renewing.
//
// Lease state lives in the bead's own metadata, not on disk — beads owns claims
// (ADR 0004), so a lease survives the machine that took it.
package main

import (
	"fmt"
	"time"
)

const (
	leaseHolderKey  = "lease_holder"
	leaseExpiresKey = "lease_expires"
	defaultLeaseTTL = 45 * time.Minute
)

type leaser struct {
	bd  bdClient
	now func() time.Time
}

// Claim takes the bead for agent, refusing if a live lease is held by someone
// else. Refusal is the point: two builders on one bead is the failure this
// prevents.
func (l leaser) Claim(id, agent string, ttl time.Duration) error {
	b, err := l.bd.show(id)
	if err != nil {
		return err
	}
	if holder, expires, ok := b.Lease(); ok && holder != agent && expires.After(l.now()) {
		return fmt.Errorf("%s: held by %s for another %s", id, holder, expires.Sub(l.now()).Round(time.Second))
	}
	if err := l.bd.claim(id); err != nil {
		return err
	}
	return l.write(b, agent, ttl)
}

// Heartbeat extends a lease the agent already holds. It refuses to extend
// someone else's — an agent whose lease was reclaimed must not silently
// resurrect it and start editing again alongside its replacement.
func (l leaser) Heartbeat(id, agent string, ttl time.Duration) error {
	b, err := l.bd.show(id)
	if err != nil {
		return err
	}
	holder, _, ok := b.Lease()
	if !ok {
		return fmt.Errorf("%s: no lease to extend", id)
	}
	if holder != agent {
		return fmt.Errorf("%s: lease belongs to %s, not %s — stop working and re-claim", id, holder, agent)
	}
	return l.write(b, agent, ttl)
}

// Release clears the lease. Called on completion and on abandonment alike; the
// bead's status says which one happened.
func (l leaser) Release(id, agent string) error {
	b, err := l.bd.show(id)
	if err != nil {
		return err
	}
	if holder, _, ok := b.Lease(); ok && holder != agent {
		return fmt.Errorf("%s: lease belongs to %s, not %s", id, holder, agent)
	}
	md := copyMD(b.Metadata)
	delete(md, leaseHolderKey)
	delete(md, leaseExpiresKey)
	return l.bd.setMetadata(id, md)
}

// Reclaimed describes one bead the sweep returned to the queue.
type Reclaimed struct {
	ID     string
	Holder string
	Late   time.Duration
}

// Reclaim sweeps in-progress beads whose lease has expired, returning them to
// open with a note naming who dropped them. This is what stops a dead agent
// from starving the fleet, and it is the only path by which work re-enters the
// queue without a human.
func (l leaser) Reclaim() ([]Reclaimed, error) {
	beads, err := l.bd.list("in_progress")
	if err != nil {
		return nil, err
	}
	var out []Reclaimed
	for _, b := range beads {
		holder, expires, ok := b.Lease()
		if !ok {
			// In progress with no lease at all: a human is working it, or an
			// agent predating leases. Leave it alone — reclaiming a human's
			// work would be worse than the starvation we are preventing.
			continue
		}
		if expires.After(l.now()) {
			continue
		}
		late := l.now().Sub(expires)
		md := copyMD(b.Metadata)
		delete(md, leaseHolderKey)
		delete(md, leaseExpiresKey)
		if err := l.bd.setMetadata(b.ID, md); err != nil {
			return out, err
		}
		if err := l.bd.reopen(b.ID); err != nil {
			return out, err
		}
		if err := l.bd.note(b.ID, fmt.Sprintf(
			"Lease reclaimed by the fleet: %s held it and stopped heartbeating; expired %s ago. "+
				"Returned to ready. Check for an abandoned worktree or branch before re-claiming.",
			holder, late.Round(time.Second))); err != nil {
			return out, err
		}
		out = append(out, Reclaimed{ID: b.ID, Holder: holder, Late: late})
	}
	return out, nil
}

func (l leaser) write(b Bead, agent string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	md := copyMD(b.Metadata)
	md[leaseHolderKey] = agent
	md[leaseExpiresKey] = l.now().Add(ttl).UTC().Format(time.RFC3339)
	return l.bd.setMetadata(b.ID, md)
}

func copyMD(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}
