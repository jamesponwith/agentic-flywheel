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
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
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
