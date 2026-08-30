// A settings file that exists and grants nothing looks identical to one that
// works — the same shape of failure as the guard that was installed and did
// nothing, one artifact over.
//
// Three builders in spotify-recsys-ecosystem burned ~$4 and 66 turns committing
// nothing: .claude/settings.json was present but held only a hooks key, so
// every write path was denied and each builder correctly failed closed. doctor
// saw the file and said nothing. It was the third setup gap in that repo in a
// row — skill absent, then present but gitignored, then present but powerless
// — and doctor caught none of them, because each artifact existed by the time
// it looked (fw-7al).
//
// So doctor reads the permissions the way the runner will, and asks for each
// thing flywheel-next actually invokes whether this file lets it happen.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// settingsPath is the one file the spawned runner reads for permissions.
// settings.local.json is gitignored and a spawner's worktree never has it, so
// a grant there helps a human at a terminal and nobody else.
const settingsPath = ".claude/settings.json"

// capability is one thing flywheel-next invokes. Tool is the permission-rule
// tool name; Cmd is the Bash command prefix, empty for tools that take none.
type capability struct {
	Tool, Cmd string
	Why       string // what the builder cannot do without it
}

// required is what a builder needs on every run, in every repo: the skill
// claims through bd, checks guard.sh before acting, edits, commits, pushes,
// and opens a PR. Miss any one and the run is a no-op that still costs money.
var required = []capability{
	{"Bash", "bd", "cannot claim or note a bead"},
	{"Bash", "tools/flywheel/guard.sh", "cannot check the kill switch or write the audit log"},
	{"Write", "", "cannot create a file"},
	{"Edit", "", "cannot change a file"},
	{"Bash", "git add", "cannot stage a change"},
	{"Bash", "git commit", "cannot commit"},
	{"Bash", "git push", "cannot push the bead branch"},
	{"Bash", "gh pr create", "cannot open the PR"},
}

// testRunners is the per-language half of the list: the gate a builder runs
// before it commits. Any one of the alternatives grants the capability.
//
// ponytail: two languages, one runner each, which is what the roster holds
// today. A repo whose gate is `make test` would need a third entry here, or a
// per-repo override in .flywheel/repo.json — add that when a repo needs it.
var testRunners = map[string][]string{
	"go":     {"go test"},
	"python": {"uv run pytest", "pytest"},
}

// probeSettings reports whether repoPath's settings.json grants what a builder
// needs, naming each capability it does not. lang picks the test runner; an
// unknown language checks everything but the runner.
func probeSettings(repoPath, lang string) (bool, string) {
	b, err := os.ReadFile(filepath.Join(repoPath, settingsPath))
	if err != nil {
		return false, "no " + settingsPath + " — the runner grants nothing without one"
	}
	var s struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return false, settingsPath + " is not valid JSON: " + err.Error()
	}

	var missing []string
	for _, c := range required {
		if !granted(s.Permissions.Allow, s.Permissions.Deny, c.Tool, c.Cmd) {
			missing = append(missing, describe(c))
		}
	}
	if runners, ok := testRunners[lang]; ok {
		found := false
		for _, r := range runners {
			if granted(s.Permissions.Allow, s.Permissions.Deny, "Bash", r) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, fmt.Sprintf("Bash(%s) — cannot run the gate", runners[0]))
		}
	}
	if len(missing) > 0 {
		return false, settingsPath + " does not grant: " + strings.Join(missing, "; ")
	}
	return true, "grants what a builder invokes"
}

func describe(c capability) string {
	if c.Cmd == "" {
		return c.Tool + " — " + c.Why
	}
	return fmt.Sprintf("%s(%s) — %s", c.Tool, c.Cmd, c.Why)
}

// granted reports whether the allow list covers tool+cmd and the deny list
// does not. A deny that covers the capability wins, as it does in the runner.
func granted(allow, deny []string, tool, cmd string) bool {
	for _, r := range deny {
		if covers(r, tool, cmd) {
			return false
		}
	}
	for _, r := range allow {
		if covers(r, tool, cmd) {
			return true
		}
	}
	return false
}

// covers reports whether one permission rule reaches the capability.
//
// Rules are `Tool` or `Tool(spec)`. A bare tool covers every use of it. For
// Bash, `spec` is either an exact command or `prefix:*`; a prefix covers the
// command when it IS the command or is a leading run of its words, so
// `Bash(git:*)` covers `git commit` and `Bash(git push --force:*)` does not
// cover `git push`. For tools without a command, any rule for the tool counts.
//
// ponytail: the runner's own prefix match is by string, not by word, so a
// `Bash(g:*)` rule would grant `git commit` there and read as absent here.
// Nobody writes that rule; treating it as absent is the safe direction.
func covers(rule, tool, cmd string) bool {
	ruleTool, spec, err := splitRule(rule)
	if err != nil || ruleTool != tool {
		return false
	}
	if spec == "" || cmd == "" {
		return true
	}
	if p, ok := strings.CutSuffix(spec, ":*"); ok {
		p = strings.TrimSpace(p)
		return p == cmd || strings.HasPrefix(cmd, p+" ")
	}
	return spec == cmd
}

// splitRule parses `Tool` or `Tool(spec)`.
func splitRule(rule string) (tool, spec string, err error) {
	rule = strings.TrimSpace(rule)
	i := strings.IndexByte(rule, '(')
	if i < 0 {
		return rule, "", nil
	}
	if !strings.HasSuffix(rule, ")") {
		return "", "", errors.New("unbalanced rule: " + rule)
	}
	return rule[:i], rule[i+1 : len(rule)-1], nil
}
