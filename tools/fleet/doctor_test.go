package main

import (
	"os"
	"path/filepath"
	"testing"
)

func repoWith(t *testing.T, paths ...string) Repo {
	t.Helper()
	dir := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(dir, p)
		if filepath.Ext(p) == "" && !hasDot(filepath.Base(p)) {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			// git cannot track an empty directory, and a real docs/adr or
			// .beads always has files in it. An empty one would correctly
			// read as missing.
			if err := os.WriteFile(filepath.Join(full, "content.md"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// present() now requires git to track the path, because "a clone would get
	// this" is the question doctor is meant to answer. The fixture therefore
	// has to be a real repo with the artifacts committed — a bare temp dir
	// models a situation that never occurs in the roster.
	for _, a := range [][]string{{"init", "-q", "-b", "main"},
		{"config", "user.email", "d@d"}, {"config", "user.name", "d"},
		{"add", "-A"}, {"commit", "-qm", "fixture"}} {
		c := inDir(dir, "git", a...)
		_ = c.Run() // an empty fixture has nothing to commit; that is fine
	}
	return Repo{Name: "r", Path: dir, Lang: "go"}
}

func hasDot(s string) bool {
	for i := 1; i < len(s); i++ {
		if s[i] == '.' {
			return true
		}
	}
	return false
}

func missingPaths(d Diagnosis) map[string]bool {
	m := map[string]bool{}
	for _, f := range d.Missing {
		m[f.Path] = true
	}
	return m
}

func TestDiagnoseFindsGaps(t *testing.T) {
	repo := repoWith(t, "SPEC.md", "CLAUDE.md", "docs/adr", ".beads", "lefthook.yml")
	d := Diagnose(repo)
	m := missingPaths(d)
	if m["SPEC.md"] || m["lefthook.yml"] {
		t.Error("reported a present artifact as missing")
	}
	if !m[".github/workflows/pr.yml"] {
		t.Error("did not notice the missing PR gate")
	}
	if d.Present != 5 {
		t.Errorf("present = %d, want 5", d.Present)
	}
}

func TestDiagnoseSkipsOtherLanguages(t *testing.T) {
	// A Go release config is not "missing" from a Python repo.
	py := repoWith(t)
	py.Lang = "python"
	if missingPaths(Diagnose(py))[".goreleaser.yaml"] {
		t.Error("reported the Go release config as missing from a Python repo")
	}
	gr := repoWith(t)
	gr.Lang = "go"
	if !missingPaths(Diagnose(gr))[".goreleaser.yaml"] {
		t.Error("did not report the Go release config missing from a Go repo")
	}
}

func TestDiagnoseOrdersRequiredFirst(t *testing.T) {
	d := Diagnose(repoWith(t))
	seenOptional := false
	for _, f := range d.Missing {
		if !f.Required {
			seenOptional = true
			continue
		}
		if seenOptional {
			t.Fatalf("required artifact %q listed after an optional one", f.Path)
		}
	}
}

func TestEveryManifestEntryExplainsItself(t *testing.T) {
	// A checklist nobody understands gets ignored.
	for _, a := range Manifest {
		if a.Why == "" {
			t.Errorf("%s has no explanation", a.Path)
		}
		if a.Stage == "" {
			t.Errorf("%s has no stage", a.Path)
		}
	}
}

func TestInstallNeverOverwrites(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SPEC.md"), []byte("from template"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "tools", "flywheel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "tools/flywheel/guard.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	repo := repoWith(t, "SPEC.md") // already has a customised SPEC
	if err := os.WriteFile(filepath.Join(repo.Path, "SPEC.md"), []byte("MINE"), 0o644); err != nil {
		t.Fatal(err)
	}

	installed, err := Install(repo, src, []Finding{
		{Path: "SPEC.md"}, {Path: "tools/flywheel/guard.sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(repo.Path, "SPEC.md"))
	if string(got) != "MINE" {
		t.Fatal("clobbered a local file — a doctor that overwrites is worse than the drift it fixes")
	}
	if len(installed) != 1 || installed[0] != "tools/flywheel/guard.sh" {
		t.Errorf("installed = %v, want just the missing file", installed)
	}
	if _, err := os.Stat(filepath.Join(repo.Path, "tools/flywheel/guard.sh")); err != nil {
		t.Error("did not install the genuinely missing artifact")
	}
}

func TestInstallCopiesDirectoriesRecursively(t *testing.T) {
	src := t.TempDir()
	deep := filepath.Join(src, ".claude/skills/flywheel-next")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := repoWith(t)
	if _, err := Install(repo, src, []Finding{{Path: ".claude/skills/flywheel-next"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(repo.Path, ".claude/skills/flywheel-next/SKILL.md"))
	if err != nil || string(b) != "skill" {
		t.Errorf("skill directory not copied: %v", err)
	}
}

func TestDiagnoseRespectsRepoRole(t *testing.T) {
	// A template with a .beads database would seed every clone with someone
	// else's issues, so it is not "missing" one (fw-fsa.6).
	tpl := repoWith(t)
	tpl.Role = "template"
	if missingPaths(Diagnose(tpl))[".beads"] {
		t.Error("reported .beads missing from a template")
	}
	inst := repoWith(t)
	if !missingPaths(Diagnose(inst))[".beads"] {
		t.Error("did not report .beads missing from an instance")
	}
}

func TestDiagnoseRespectsSkippedStages(t *testing.T) {
	// A training project with nothing deployed is not BEHIND on Operate; it has
	// no use for it. A checklist reporting a permanent false gap gets ignored.
	r := repoWith(t)
	r.SkipStages = []string{"operate"}
	m := missingPaths(Diagnose(r))
	if m["docs/slo.yml"] || m[".github/workflows/operate.yml"] {
		t.Error("reported Operate artifacts as gaps in a repo that skips the stage")
	}
	if !missingPaths(Diagnose(repoWith(t)))["docs/slo.yml"] {
		t.Error("a repo that did NOT skip operate should still report it")
	}
}

func TestInstallRefusesArtifactsNeedingAdaptation(t *testing.T) {
	// -fix once installed the Go template's operate.yml into the router, where
	// it referenced a ./tools/watch that does not exist. An artifact installed
	// in a form that cannot work is worse than a reported gap, because the
	// checklist then claims "complete".
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ".github/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".github/workflows/operate.yml"), []byte("go run ./tools/watch"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SPEC.md"), []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := repoWith(t)
	installed, err := Install(repo, src, []Finding{
		{Path: ".github/workflows/operate.yml", Manual: true},
		{Path: "SPEC.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range installed {
		if p == ".github/workflows/operate.yml" {
			t.Error("installed an artifact that needs hand adaptation")
		}
	}
	if len(installed) != 1 || installed[0] != "SPEC.md" {
		t.Errorf("installed = %v, want only SPEC.md", installed)
	}
}

func TestManifestMarksTheArtifactsThatBitUs(t *testing.T) {
	want := map[string]bool{".github/workflows/operate.yml": true, "docs/slo.yml": true}
	for _, a := range Manifest {
		if want[a.Path] && !a.NeedsAdaptation {
			t.Errorf("%s is copyable per the manifest, but it is not portable between languages", a.Path)
		}
	}
}

// doctor used os.Stat, which answers "a filename existed on this filesystem at
// this instant" — not "a clone would get this". Three repos reported complete
// on artifacts that were never committed, including drift.yml, the check meant
// to catch exactly that class of drift.
func TestPresentRequiresGitToTrackIt(t *testing.T) {
	dir := t.TempDir()
	for _, a := range [][]string{{"init", "-q", "-b", "main"},
		{"config", "user.email", "d@d"}, {"config", "user.name", "d"}} {
		c := inDir(dir, "git", a...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"add", "tracked.md"}, {"commit", "-qm", "add"}} {
		c := inDir(dir, "git", a...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}

	if !present(dir, "tracked.md") {
		t.Error("a tracked file was reported missing")
	}
	if present(dir, "untracked.md") {
		t.Error("an untracked file was reported present — this is the bug that made three repos claim parity they did not have")
	}
	if present(dir, "absent.md") {
		t.Error("a file that does not exist was reported present")
	}
}
