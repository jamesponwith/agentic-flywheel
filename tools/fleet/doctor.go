// doctor: tell a repo which stages it is missing, and optionally install them.
//
// A template repo solves onboarding once and then rots. The three instances
// here forked at three different versions, and none of them gets the v2 work
// unless something carries it forward. This is that something.
//
// Deliberately data-driven: the manifest below IS the definition of "inside the
// flywheel". Adding a stage artifact means adding a line, not writing code, and
// the manifest doubles as documentation of what a flywheel repo actually is.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Artifact is one file or directory a flywheel repo should carry.
type Artifact struct {
	Path     string // relative to the repo root
	Stage    string // intent | build | validate | release | operate | learn | agents
	Required bool   // false = expected only once the stage is adopted
	Lang     string // "" = all languages, else "go" / "python"
	Why      string // shown when it is missing; a checklist nobody understands gets ignored
	// InstanceOnly artifacts are not expected in a template. A template with a
	// .beads database would seed every clone with someone else's issues.
	InstanceOnly bool
	// NeedsAdaptation marks artifacts that cannot be copied verbatim between
	// repos. operate.yml in the Go template runs `go run ./tools/watch`, a path
	// only the template has; -fix once installed exactly that into the router
	// and produced a workflow that fails on first run. An artifact installed in
	// a form that cannot work is worse than a reported gap, because the
	// checklist then says "complete" (fw-fsa.7).
	NeedsAdaptation bool
}

// Manifest is what "inside the flywheel" means, as data.
var Manifest = []Artifact{
	{"SPEC.md", "intent", true, "", "no spec means the agent has no intent to read", false, false},
	{"CLAUDE.md", "intent", true, "", "conventions the agent loads before writing code", false, false},
	{"docs/adr", "intent", true, "", "decisions that would cost >5 minutes to re-derive", false, false},
	{".beads", "intent", true, "", "no bead, no build", true, false},

	{"lefthook.yml", "build", true, "", "the inner-loop gate; without it nothing is checked before commit", false, false},

	{".github/workflows/pr.yml", "validate", true, "", "no green, no merge", false, false},
	{".gitattributes", "validate", false, "", "union merge on agent logs so parallel agents do not conflict", false, false},

	{".github/workflows/release.yml", "release", false, "", "semver tag to signed binaries", false, false},
	{".goreleaser.yaml", "release", false, "go", "release build config", false, false},

	{"docs/slo.yml", "operate", false, "", "the Operate contract: what counts as a breach", false, true},
	{".github/workflows/operate.yml", "operate", false, "", "probes the deployment and files incidents", false, true},

	{".github/workflows/learn.yml", "learn", true, "", "weekly DORA + agent snapshot; the trend line", false, false},
	{"docs/dora.html", "learn", false, "", "renders the snapshot", false, false},

	{"tools/flywheel/guard.sh", "agents", true, "", "kill switch, audit log, review ledger", false, false},
	{".claude/skills/flywheel-next", "agents", false, "", "the unit of autonomous work", false, false},
	{".claude/skills/flywheel-review", "agents", false, "", "three-lens review panel", false, false},
	{".flywheel/README.md", "agents", false, "", "explains the local agent state directory", false, false},
}

type Diagnosis struct {
	Repo    string    `json:"repo"`
	Missing []Finding `json:"missing"`
	Present int       `json:"present"`
}

type Finding struct {
	Path     string `json:"path"`
	Stage    string `json:"stage"`
	Required bool   `json:"required"`
	Why      string `json:"why"`
	// Manual artifacts must be adapted by hand; -fix refuses to install them.
	Manual bool `json:"manual,omitempty"`
}

// Diagnose reports what a repo is missing. It never modifies anything.
func Diagnose(repo Repo) Diagnosis {
	d := Diagnosis{Repo: repo.Name}
	for _, a := range Manifest {
		if a.Lang != "" && repo.Lang != "" && a.Lang != repo.Lang {
			continue // a Go release config is not missing from a Python repo
		}
		if a.InstanceOnly && repo.Role == "template" {
			continue // a template with a beads DB seeds every clone with its issues
		}
		if repo.skips(a.Stage) {
			continue // this repo has declared it has no use for the stage
		}
		if _, err := os.Stat(filepath.Join(repo.Path, a.Path)); err == nil {
			d.Present++
			continue
		}
		d.Missing = append(d.Missing, Finding{a.Path, a.Stage, a.Required, a.Why, a.NeedsAdaptation})
	}
	sort.SliceStable(d.Missing, func(i, j int) bool {
		if d.Missing[i].Required != d.Missing[j].Required {
			return d.Missing[i].Required // required first
		}
		return d.Missing[i].Path < d.Missing[j].Path
	})
	return d
}

// Install copies missing artifacts from a source repo. It refuses to overwrite:
// a doctor that clobbers local customisation is worse than the drift it fixes.
func Install(repo Repo, source string, only []Finding) ([]string, error) {
	var done []string
	for _, f := range only {
		if f.Manual {
			// Refuse rather than install something that cannot work here. A
			// wrongly-installed artifact makes the checklist say "complete".
			continue
		}
		src := filepath.Join(source, f.Path)
		if _, err := os.Stat(src); err != nil {
			continue // the source does not have it either; not this tool's problem
		}
		dst := filepath.Join(repo.Path, f.Path)
		if _, err := os.Stat(dst); err == nil {
			continue // never overwrite
		}
		if err := copyPath(src, dst); err != nil {
			return done, fmt.Errorf("%s: %w", f.Path, err)
		}
		done = append(done, f.Path)
	}
	return done, nil
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// skips reports whether the repo declared no use for a stage. "Not applicable"
// is a third state alongside present and missing: a training project with
// nothing deployed is not BEHIND on Operate, it simply has no use for it, and a
// checklist that reports a permanent false gap trains people to ignore it.
func (r Repo) skips(stage string) bool {
	for _, s := range r.SkipStages {
		if s == stage {
			return true
		}
	}
	return false
}

func (d Diagnosis) String() string {
	if len(d.Missing) == 0 {
		return fmt.Sprintf("  %-22s complete (%d artifacts)", d.Repo, d.Present)
	}
	var b strings.Builder
	req := 0
	for _, m := range d.Missing {
		if m.Required {
			req++
		}
	}
	fmt.Fprintf(&b, "  %-22s %d present, %d missing (%d required)\n", d.Repo, d.Present, len(d.Missing), req)
	for _, m := range d.Missing {
		mark := " "
		if m.Required {
			mark = "!"
		}
		fmt.Fprintf(&b, "    %s %-38s %-9s %s\n", mark, m.Path, m.Stage, m.Why)
	}
	return strings.TrimRight(b.String(), "\n")
}
