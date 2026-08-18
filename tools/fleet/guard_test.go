package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFleetStopHonoursFlywheelHomeWithoutAHomeDir(t *testing.T) {
	// Nesting the FLYWHEEL_HOME lookup inside `if err == nil` on UserHomeDir
	// meant that with HOME unset — the stripped environment unattended
	// operation runs in — the fleet-wide kill switch was skipped entirely.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "STOP"), []byte("fleet halt"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLYWHEEL_HOME", dir)
	t.Setenv("HOME", "")

	why, halted := stopped(nil)
	if !halted {
		t.Fatal("fleet-wide STOP ignored when HOME is unset — the switch is off exactly where it matters")
	}
	if why == "" {
		t.Error("halted without saying why")
	}
}

func TestFleetStopIsQuietWhenNoSwitchIsSet(t *testing.T) {
	t.Setenv("FLYWHEEL_HOME", t.TempDir())
	if _, halted := stopped(nil); halted {
		t.Error("reported stopped with no STOP file anywhere")
	}
}
