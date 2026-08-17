// Thin wrapper over the bd CLI. The fleet talks to beads through its command
// line rather than its database: bd owns the work graph (ADR 0004), and
// shelling out keeps us insulated from how it stores things.
package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Bead is the subset of a bd issue the fleet reasons about.
type Bead struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Status   string            `json:"status"`
	Priority int               `json:"priority"`
	Type     string            `json:"issue_type"`
	Labels   []string          `json:"labels"`
	Metadata map[string]string `json:"metadata"`
	Updated  time.Time         `json:"updated_at"`
}

// Lease reports the current lease on a bead, if any.
func (b Bead) Lease() (holder string, expires time.Time, ok bool) {
	holder = b.Metadata["lease_holder"]
	raw := b.Metadata["lease_expires"]
	if holder == "" || raw == "" {
		return "", time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", time.Time{}, false
	}
	return holder, t, true
}

func (b Bead) HasLabel(l string) bool {
	for _, got := range b.Labels {
		if got == l {
			return true
		}
	}
	return false
}

// runner executes a bd command. Injected so tests never touch a real database.
type runner func(dir string, args ...string) ([]byte, error)

func execBD(dir string, args ...string) ([]byte, error) {
	cmd := inDir(dir, "bd", args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errorsAs(err, &ee) {
			return out, fmt.Errorf("bd %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return out, fmt.Errorf("bd %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// errorsAs avoids importing errors just for one call site.
func errorsAs(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

type bdClient struct {
	dir string
	run runner
}

func (c bdClient) ready() ([]Bead, error) {
	out, err := c.run(c.dir, "ready", "--json")
	if err != nil {
		return nil, err
	}
	return decodeBeads(out)
}

func (c bdClient) list(status string) ([]Bead, error) {
	out, err := c.run(c.dir, "list", "--status", status, "--json")
	if err != nil {
		return nil, err
	}
	return decodeBeads(out)
}

func (c bdClient) show(id string) (Bead, error) {
	out, err := c.run(c.dir, "show", id, "--json")
	if err != nil {
		return Bead{}, err
	}
	bs, err := decodeBeads(out)
	if err != nil {
		return Bead{}, err
	}
	if len(bs) == 0 {
		return Bead{}, fmt.Errorf("%s: not found", id)
	}
	return bs[0], nil
}

// decodeBeads accepts either a single object or an array — bd returns both
// shapes depending on the subcommand.
func decodeBeads(b []byte) ([]Bead, error) {
	b = []byte(strings.TrimSpace(string(b)))
	// An empty result may arrive as "", "null", or "[]". Without the null case
	// json.Unmarshal happily decodes it into a zero-value Bead, and the
	// coordinator then allocates a bead with no id — a phantom assignment.
	if len(b) == 0 || string(b) == "null" {
		return nil, nil
	}
	if b[0] == '[' {
		var bs []Bead
		return bs, json.Unmarshal(b, &bs)
	}
	var one Bead
	if err := json.Unmarshal(b, &one); err != nil {
		return nil, err
	}
	return []Bead{one}, nil
}

func (c bdClient) setMetadata(id string, md map[string]string) error {
	b, err := json.Marshal(md)
	if err != nil {
		return err
	}
	_, err = c.run(c.dir, "update", id, "--metadata", string(b))
	return err
}

func (c bdClient) claim(id string) error {
	_, err := c.run(c.dir, "update", id, "--claim")
	return err
}

func (c bdClient) reopen(id string) error {
	_, err := c.run(c.dir, "update", id, "--status", "open")
	return err
}

func (c bdClient) note(id, text string) error {
	_, err := c.run(c.dir, "note", id, text)
	return err
}
