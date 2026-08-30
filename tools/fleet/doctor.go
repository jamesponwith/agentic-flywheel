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
	"encoding/json"
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
	{".github/workflows/signals.yml", "intent", false, "", "external signals become beads instead of being ignored", false, false},
	{"docs/dora.html", "learn", false, "", "renders the snapshot", false, false},
	{".github/workflows/drift.yml", "distribution", false, "", "notices when this repo falls behind the flywheel", false, false},

	{"tools/flywheel/guard.sh", "agents", true, "", "kill switch, audit log, review ledger", false, false},
	{"tools/flywheel/flaky.sh", "validate", false, "", "tells a flaky test from a broken one, and puts a deadline on it", false, false},
	{".claude/skills/flywheel-next", "agents", false, "", "the unit of autonomous work", false, false},
	{".claude/skills/flywheel-review", "agents", false, "", "three-lens review panel", false, false},
	// NeedsAdaptation: the allow list names the repo's own gate and the deny
	// list is its policy. A copied one that names the wrong runner would fail
	// the settings probe on the next run, but better not to install it at all.
	{".claude/settings.json", "agents", false, "", "the permissions a spawned builder runs under; without them it fails closed", false, true},
	{".flywheel/README.md", "agents", false, "", "explains the local agent state directory", false, false},
}

type Diagnosis struct {
	Repo    string    `json:"repo"`
	Missing []Finding `json:"missing"`
	Present int       `json:"present"`
	// Probes are the artifacts that exist and were asked to do their job.
	// "Present" was already hardened once, from "a filename exists" to "git
	// tracks it"; this is the next rung, and the only one that can catch a
	// guard disabled by a config key nobody validates (fw-ybs).
	Probes []Probe `json:"probes,omitempty"`
}

type Finding struct {
	Path     string `json:"path"`
	Stage    string `json:"stage"`
	Required bool   `json:"required"`
	Why      string `json:"why"`
	// Manual artifacts must be adapted by hand; -fix refuses to install them.
	Manual bool `json:"manual,omitempty"`
}

// Diagnose reports what a repo is missing, and what is present but not working.
// It writes nothing to the repo itself, but it does run the pre-push hook to
// ask it for a verdict — the only way to tell an installed guard from an
// installed-looking one. See guardprobe.go.
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
		if present(repo.Path, a.Path) {
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
	d.Probes = probes(repo)
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
// present reports whether an artifact exists AND is tracked by git.
//
// os.Stat alone answered "a filename existed on this filesystem at this
// instant", which is not the question. Three repos reported "complete" on
// artifacts that were never committed — including drift.yml, the check meant
// to catch exactly that, which 404s on GitHub in those repos. What a clone and
// CI see is what matters, so an untracked file is a gap.
func present(repoPath, rel string) bool {
	if _, err := os.Stat(filepath.Join(repoPath, rel)); err != nil {
		return false
	}
	// A directory counts as tracked if git tracks anything inside it.
	out, err := inDir(repoPath, "git", "ls-files", "--error-unmatch", rel).Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return true
	}
	out, err = inDir(repoPath, "git", "ls-files", "--", rel).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func (r Repo) skips(stage string) bool {
	for _, s := range r.SkipStages {
		if s == stage {
			return true
		}
	}
	return false
}

// Failing returns the probes that did not get the verdict they wanted.
func (d Diagnosis) Failing() []Probe {
	var out []Probe
	for _, p := range d.Probes {
		if !p.OK {
			out = append(out, p)
		}
	}
	return out
}

// String is read by drift.yml, which treats the literal "complete (" as parity
// and files nothing when it sees it. A failing probe must therefore keep that
// token off the line: a checklist that says complete over a red check is the
// exact failure the probe was added to end.
func (d Diagnosis) String() string {
	failing := d.Failing()
	var b strings.Builder
	switch {
	case len(d.Missing) == 0 && len(failing) == 0:
		fmt.Fprintf(&b, "  %-22s complete (%d artifacts)\n", d.Repo, d.Present)
	case len(d.Missing) == 0:
		fmt.Fprintf(&b, "  %-22s %d present, %d not working\n", d.Repo, d.Present, len(failing))
	default:
		req := 0
		for _, m := range d.Missing {
			if m.Required {
				req++
			}
		}
		fmt.Fprintf(&b, "  %-22s %d present, %d missing (%d required)", d.Repo, d.Present, len(d.Missing), req)
		if len(failing) > 0 {
			fmt.Fprintf(&b, ", %d not working", len(failing))
		}
		b.WriteString("\n")
	}
	for _, m := range d.Missing {
		mark := " "
		if m.Required {
			mark = "!"
		}
		fmt.Fprintf(&b, "    %s %-38s %-9s %s\n", mark, m.Path, m.Stage, m.Why)
	}
	for _, p := range failing {
		fmt.Fprintf(&b, "    x %-38s %-9s %s\n", p.Name, "agents", p.Why)
	}
	return strings.TrimRight(b.String(), "\n")
}

// selfRoster builds a one-repo roster for the current working directory, so an
// instance can check its own drift without carrying a fleet roster it has no
// business knowing about.
//
// Role and skipped stages are read from .flywheel/repo.json when present —
// otherwise a training project would be told every week that it is missing an
// Operate stage it has no use for, which is how a check becomes noise.
func selfRoster() Roster {
	repo := Repo{Name: filepath.Base(mustCwd()), Path: mustCwd()}
	if b, err := os.ReadFile(filepath.Join(repo.Path, ".flywheel", "repo.json")); err == nil {
		var decl struct {
			Role       string   `json:"role"`
			Lang       string   `json:"lang"`
			SkipStages []string `json:"skip_stages"`
		}
		if json.Unmarshal(b, &decl) == nil {
			repo.Role, repo.Lang, repo.SkipStages = decl.Role, decl.Lang, decl.SkipStages
		}
	}
	if repo.Lang == "" {
		if _, err := os.Stat(filepath.Join(repo.Path, "go.mod")); err == nil {
			repo.Lang = "go"
		} else if _, err := os.Stat(filepath.Join(repo.Path, "pyproject.toml")); err == nil {
			repo.Lang = "python"
		}
	}
	return Roster{
		Caps:  Caps{ReviewWeightPerNight: 8, ConcurrentBuilders: 1, ReposPerNight: 1},
		Repos: []Repo{repo},
	}
}

func mustCwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}
