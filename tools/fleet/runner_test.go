package main

import (
	"strings"
	"testing"
)

func TestRunnerArgv(t *testing.T) {
	tests := []struct {
		name   string
		runner Runner
		want   string
	}{
		{"default is claude, but as a default not an assumption",
			Runner{}, "-p /flywheel-next x-1"},
		{"a different agent is a config edit, not a code change",
			Runner{Cmd: "opencode", Args: []string{"run", "{prompt}"}}, "run /flywheel-next x-1"},
		{"prompt can be embedded mid-argument",
			Runner{Cmd: "a", Args: []string{"--task={prompt}"}}, "--task=/flywheel-next x-1"},
		{"a runner that forgets {prompt} still receives the work",
			Runner{Cmd: "a", Args: []string{"--quiet"}}, "--quiet /flywheel-next x-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(tt.runner.argv("/flywheel-next x-1"), " ")
			if got != tt.want {
				t.Errorf("argv = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunnerEnvOverridesCommandEverywhere(t *testing.T) {
	// One variable switches the whole flywheel to another agent — including
	// shell hooks, which cannot read the roster.
	t.Setenv("FLYWHEEL_AGENT_CMD", "myagent")
	if got := (Runner{Cmd: "claude"}).resolved().Cmd; got != "myagent" {
		t.Errorf("cmd = %q, want myagent", got)
	}
	if got := (Runner{}).resolved().Cmd; got != "myagent" {
		t.Errorf("empty runner ignored the override: %q", got)
	}
}

func TestRunnerStdinDefaultsToQuiet(t *testing.T) {
	// Git hands a pre-push hook the ref list on stdin. An agent CLI reads it
	// instead of its prompt — the bug that kept the review panel dead.
	if !(Runner{}).quietStdin() {
		t.Error("default runner inherits stdin; that is the bug this exists to prevent")
	}
	if !(Runner{Cmd: "a", Stdin: ""}).quietStdin() {
		t.Error("unset stdin should be quiet, not inherited")
	}
	if (Runner{Cmd: "a", Stdin: "inherit"}).quietStdin() {
		t.Error("explicit inherit was ignored")
	}
}

func TestRunnerDefaultsFillPartialDeclarations(t *testing.T) {
	// A roster naming only a command must still work.
	r := Runner{Cmd: "myagent"}.resolved()
	if r.Cmd != "myagent" || len(r.Args) == 0 {
		t.Errorf("partial runner not filled: %+v", r)
	}
}
