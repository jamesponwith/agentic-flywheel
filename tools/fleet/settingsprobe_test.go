// The case that shipped is a settings.json that exists and grants nothing. A
// probe that only passes against a working file is `return true` with extra
// steps, so most of these are the ways a present file fails.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realSettings is this repo's own settings.json. If the probe fails it, either
// the probe is wrong or the fleet's own builders cannot commit — both worth a
// red test.
func realSettings(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", settingsPath))
	if err != nil {
		t.Fatalf("settings.json is not where this repo keeps it: %v", err)
	}
	return string(b)
}

func writeSettings(t *testing.T, repo, content string) {
	t.Helper()
	p := filepath.Join(repo, settingsPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fullGrant is every rule a Go builder needs, one per capability, so a row
// can drop or swap exactly the one it is about.
var fullGrant = []string{
	"Bash(bd:*)", "Bash(tools/flywheel/guard.sh:*)", "Write", "Edit",
	"Bash(git add:*)", "Bash(git commit:*)", "Bash(git push:*)", "Bash(gh pr create:*)", "Bash(go test:*)",
}

// settingsWith builds a settings.json from a permissions block.
func settingsWith(t *testing.T, p permissions) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"permissions": p})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// without is fullGrant minus the named rules.
func without(drop ...string) []string {
	var out []string
	for _, r := range fullGrant {
		skip := false
		for _, d := range drop {
			skip = skip || r == d
		}
		if !skip {
			out = append(out, r)
		}
	}
	return out
}

func TestProbeSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings string // "" = no file at all
		lang     string
		want     bool
		wantWhy  string
		// notWhy is a capability the reason must NOT name: a probe that lists
		// everything as missing is no more actionable than "not working".
		notWhy string
	}{
		{
			name:     "the real file grants what a builder invokes",
			settings: "REAL", lang: "go", want: true,
			wantWhy: "grants",
		},
		{
			// The case that shipped: hooks only, not even a permissions key.
			// Three builders, 66 turns, nothing committed (fw-7al).
			name:     "present but powerless",
			settings: `{"hooks":{"SessionStart":[]}}`, lang: "python", want: false,
			wantWhy: "Bash(git commit)",
		},
		{
			name: "absent", settings: "", lang: "go", want: false,
			wantWhy: "no " + settingsPath,
		},
		{
			name: "not JSON", settings: "{", lang: "go", want: false,
			wantWhy: "not valid JSON",
		},
		{
			name:     "one capability short, and it names that one",
			settings: settingsWith(t, permissions{Allow: without("Bash(git commit:*)")}),
			lang:     "go", want: false,
			wantWhy: "Bash(git commit) — cannot commit",
			notWhy:  "Bash(bd)",
		},
		{
			// A deny wins over an allow in the runner, so it wins here.
			name:     "allowed then denied",
			settings: settingsWith(t, permissions{Allow: []string{"Bash"}, Deny: []string{"Bash(git commit:*)"}}),
			lang:     "go", want: false,
			wantWhy: "Bash(git commit)",
		},
		{
			// The deny list in this very repo: a narrower deny does not cover
			// the capability. Reporting it would be a permanent false gap.
			name: "a narrower deny does not revoke the capability",
			settings: settingsWith(t, permissions{Allow: []string{"Bash", "Write", "Edit"},
				Deny: []string{"Bash(git push --force:*)", "Bash(git push origin main:*)"}}),
			lang: "go", want: true,
		},
		{
			// ADR 0015: builders do not write under .claude/. A repo that
			// enforces it with a scoped deny still grants Write and Edit
			// everywhere else, and nagging it to drop the deny is backwards.
			name: "a scoped deny on Write or Edit is a guard, not a revocation",
			settings: settingsWith(t, permissions{Allow: fullGrant,
				Deny: []string{"Write(.claude/**)", "Edit(.claude/**)"}}),
			lang: "go", want: true,
		},
		{
			// The other direction: a Write scoped to src/ cannot touch .beads
			// or the outbox — present but powerless, one rule at a time.
			name:     "a scoped allow on Write is not a grant",
			settings: settingsWith(t, permissions{Allow: append(without("Write"), "Write(src/**)")}),
			lang:     "go", want: false,
			wantWhy: "Write — cannot create a file",
			notWhy:  "Edit",
		},
		{
			// `Bash(git:*)` reaches `git commit`; a bare `Bash` reaches all.
			name: "a broader allow covers the capability",
			settings: settingsWith(t, permissions{Allow: []string{"Bash(bd:*)", "Bash(tools/flywheel/guard.sh:*)",
				"Write", "Edit", "Bash(git:*)", "Bash(gh:*)", "Bash(go:*)"}}),
			lang: "go", want: true,
		},
		{
			// defaultMode grants without an allow entry: acceptEdits is
			// Write and Edit, bypassPermissions is everything.
			name:     "acceptEdits grants the edit tools",
			settings: settingsWith(t, permissions{Allow: without("Write", "Edit"), DefaultMode: "acceptEdits"}),
			lang:     "go", want: true,
		},
		{
			name:     "bypassPermissions grants everything",
			settings: settingsWith(t, permissions{DefaultMode: "bypassPermissions"}),
			lang:     "go", want: true,
		},
		{
			name:     "a deny still wins under bypassPermissions",
			settings: settingsWith(t, permissions{DefaultMode: "bypassPermissions", Deny: []string{"Bash(gh pr create:*)"}}),
			lang:     "go", want: false,
			wantWhy: "Bash(gh pr create)",
		},
		{
			// The Go runner granted in a Python repo is the wrong gate.
			name:     "the test runner is per language",
			settings: settingsWith(t, permissions{Allow: fullGrant}), lang: "python", want: false,
			wantWhy: "Bash(uv run pytest) — cannot run the gate",
		},
		{
			// Either spelling of the Python gate will do.
			name:     "any of the language's runners will do",
			settings: settingsWith(t, permissions{Allow: append(without("Bash(go test:*)"), "Bash(pytest:*)")}),
			lang:     "python", want: true,
		},
		{
			// An unknown language has no runner to ask for; do not invent one.
			name:     "an unknown language is not asked for a runner",
			settings: settingsWith(t, permissions{Allow: without("Bash(go test:*)")}),
			lang:     "rust", want: true,
		},
		{
			// An exact rule is not a prefix: `Bash(git commit)` grants the bare
			// command only, and a builder always passes -m.
			name:     "an exact rule for the bare command still counts",
			settings: settingsWith(t, permissions{Allow: append(without("Bash(git commit:*)"), "Bash(git commit)")}),
			lang:     "go", want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			switch tt.settings {
			case "":
			case "REAL":
				writeSettings(t, dir, realSettings(t))
			default:
				writeSettings(t, dir, tt.settings)
			}
			ok, why := probeSettings(dir, tt.lang)
			if ok != tt.want {
				t.Errorf("probeSettings ok = %v, want %v (why: %s)", ok, tt.want, why)
			}
			if !strings.Contains(why, tt.wantWhy) {
				t.Errorf("why = %q, want it to mention %q", why, tt.wantWhy)
			}
			if tt.notWhy != "" && strings.Contains(why, tt.notWhy) {
				t.Errorf("why = %q names %q, which the file grants", why, tt.notWhy)
			}
		})
	}
}

// The acceptance criterion: a repo at full artifact parity with a powerless
// settings.json is reported as not working, and the report names what is
// absent — not "settings.json missing", which it is not.
func TestDiagnoseWillNotSayCompleteOverAPowerlessSettings(t *testing.T) {
	t.Setenv("CI", "true") // no hook to probe; the settings probe decides alone

	r := fullRepo(t)
	writeSettings(t, r.Path, `{"hooks":{"SessionStart":[{"hooks":[{"command":"bd prime --hook-json","type":"command"}],"matcher":""}]}}`)
	d := Diagnose(r)

	if len(d.Missing) != 0 {
		t.Fatalf("fixture is not at artifact parity, so this proves nothing: %v", d.Missing)
	}
	if len(d.Failing()) != 1 {
		t.Fatalf("failing probes = %v, want the settings probe", d.Probes)
	}
	s := d.String()
	if strings.Contains(s, "complete (") {
		t.Errorf("reported complete over a file that grants nothing:\n%s", s)
	}
	if !strings.Contains(s, "not working") || !strings.Contains(s, "settings grants") {
		t.Errorf("did not name the failing check:\n%s", s)
	}
	if !strings.Contains(s, "Write") || !strings.Contains(s, "Bash(git commit)") {
		t.Errorf("did not name the capabilities that are absent:\n%s", s)
	}
}

// The second of fw-7al's three gaps: present on disk, gitignored. That is the
// manifest's finding — a clone would not get it — and the probe must not read
// the untracked file off disk and call the repo working over the top of it.
func TestDiagnoseReportsAnUntrackedSettingsAsMissingNotWorking(t *testing.T) {
	t.Setenv("CI", "true")

	r := fullRepo(t)
	for _, a := range [][]string{{"rm", "-q", "--cached", settingsPath}, {"commit", "-qm", "untrack"}} {
		if out, err := inDir(r.Path, "git", a...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	d := Diagnose(r)

	if !missingPaths(d)[settingsPath] {
		t.Errorf("an untracked settings.json was not reported missing: %v", d.Missing)
	}
	for _, p := range d.Probes {
		if p.Name == "settings grants" {
			t.Errorf("probed a file a clone would not have, and said %q", p.Why)
		}
	}
}

func TestCovers(t *testing.T) {
	tests := []struct {
		rule, tool, cmd string
		want            bool
	}{
		{"Bash", "Bash", "git commit", true},
		{"Bash(git commit:*)", "Bash", "git commit", true},
		{"Bash(git:*)", "Bash", "git commit", true},
		{"Bash(git commit)", "Bash", "git commit", true},
		{"Bash(git commit -m x)", "Bash", "git commit", false},
		{"Bash(git push --force:*)", "Bash", "git push", false},
		{"Bash(gitk:*)", "Bash", "git commit", false},
		{"Bash(go test:*)", "Bash", "gofmt", false},
		{"Write", "Write", "", true},
		{"Write(src/**)", "Write", "", false}, // scoped: neither a full grant nor a revocation
		{"Edit", "Write", "", false},
		{"Bash(git commit:*", "Bash", "git commit", false}, // unbalanced: not a rule
		{"mcp__blackbird__blackbird_agent_register", "Bash", "bd", false},
	}
	for _, tt := range tests {
		if got := covers(tt.rule, tt.tool, tt.cmd); got != tt.want {
			t.Errorf("covers(%q, %q, %q) = %v, want %v", tt.rule, tt.tool, tt.cmd, got, tt.want)
		}
	}
}
