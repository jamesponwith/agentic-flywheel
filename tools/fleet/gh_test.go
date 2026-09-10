package main

import "testing"

func TestGhRepo(t *testing.T) {
	tests := []struct {
		name string
		repo Repo
		want string
	}{
		{"owner from the repo", Repo{Name: "router", Owner: "some-org"}, "some-org/router"},
		{"no owner falls back to the fleet's own account", Repo{Name: "router"}, DefaultGitHubOwner + "/router"},
	}
	for _, tt := range tests {
		if got := ghRepo(tt.repo); got != tt.want {
			t.Errorf("%s: ghRepo(%+v) = %q, want %q", tt.name, tt.repo, got, tt.want)
		}
	}
}
