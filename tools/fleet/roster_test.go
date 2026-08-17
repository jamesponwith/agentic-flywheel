package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("FLYWHEEL_WORKSPACE", "")
	tests := []struct {
		name, in, root, want string
	}{
		{"absolute is honoured as-is", "/opt/weird/place", "~/Workspace", "/opt/weird/place"},
		{"tilde expands", "~/elsewhere/repo", "", filepath.Join(home, "elsewhere/repo")},
		{"relative joins the root", "router", "/w", "/w/router"},
		{"root itself expands", "router", "~/Workspace", filepath.Join(home, "Workspace", "router")},
		{"empty stays empty", "", "/w", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandPath(tt.in, tt.root); got != tt.want {
				t.Errorf("expandPath(%q, %q) = %q, want %q", tt.in, tt.root, got, tt.want)
			}
		})
	}
}

func TestExpandPathUsesEnvWorkspace(t *testing.T) {
	t.Setenv("FLYWHEEL_WORKSPACE", "/env/ws")
	if got := expandPath("router", ""); got != "/env/ws/router" {
		t.Errorf("got %q, want /env/ws/router", got)
	}
}

func TestLoadRosterExpandsPaths(t *testing.T) {
	// The committed roster must not need editing on a different machine.
	dir := t.TempDir()
	p := filepath.Join(dir, "roster.json")
	if err := os.WriteFile(p, []byte(`{
      "workspace_root": "/w",
      "caps": {"prs_per_night":1,"concurrent_builders":1,"repos_per_night":1},
      "repos": [{"name":"a","path":"a"},{"name":"b","path":"/abs/b"}],
      "agents": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRoster(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Repos[0].Path != "/w/a" {
		t.Errorf("relative path = %q, want /w/a", r.Repos[0].Path)
	}
	if r.Repos[1].Path != "/abs/b" {
		t.Errorf("absolute path = %q, want it untouched", r.Repos[1].Path)
	}
}

func TestCommittedRosterHasNoHostPaths(t *testing.T) {
	// This repo is public. A roster that hardcodes /home/<someone> publishes
	// the maintainer's username and only works on one machine.
	b, err := os.ReadFile("../../.flywheel/roster.json")
	if err != nil {
		t.Skip("roster not present")
	}
	if strings.Contains(string(b), "/home/") || strings.Contains(string(b), "/Users/") {
		t.Errorf("committed roster contains an absolute host path:\n%s", b)
	}
}
