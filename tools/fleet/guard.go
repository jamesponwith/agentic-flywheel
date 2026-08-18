package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// stopped reports whether the kill switch is set, and why. Checked at the top
// of every allocation cycle: a coordinator that keeps allocating after the
// switch is thrown is not a coordinator, it is a runaway.
//
// The switch is a file on purpose. Stopping the fleet must not require a
// database, a network, or a push — see tools/flywheel/guard.sh.
func stopped(repoPaths []string) (string, bool) {
	// FLYWHEEL_HOME is checked even when the home directory cannot be resolved.
	// Nesting this inside `if err == nil` meant that with HOME unset — which is
	// exactly the stripped environment unattended operation runs in — the
	// fleet-wide kill switch was skipped entirely.
	fleetHome := os.Getenv("FLYWHEEL_HOME")
	if fleetHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			fleetHome = filepath.Join(home, ".flywheel")
		}
	}
	if fleetHome != "" {
		if b, err := os.ReadFile(filepath.Join(fleetHome, "STOP")); err == nil {
			return "fleet-wide: " + strings.TrimSpace(string(b)), true
		}
	}
	for _, p := range repoPaths {
		if b, err := os.ReadFile(filepath.Join(p, ".flywheel", "STOP")); err == nil {
			return fmt.Sprintf("%s: %s", filepath.Base(p), strings.TrimSpace(string(b))), true
		}
	}
	return "", false
}

// repoPaths of every non-paused repo — a paused repo's stop file is redundant.
func (r Roster) activePaths() []string {
	var out []string
	for _, repo := range r.Repos {
		if !repo.Paused {
			out = append(out, repo.Path)
		}
	}
	return out
}
