package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// driftRepo builds a scratch repo with one commit on main, then leaves HEAD on
// a branch off it. Every drift test needs the same two-ref shape.
func driftRepo(t *testing.T, seed map[string]string) string {
	t.Helper()
	dir := gitRepo(t)
	for p, c := range seed {
		addFile(t, dir, p, c)
	}
	commitAll(t, dir, "initial")
	run(t, dir, "checkout", "-q", "-b", "work")
	return dir
}

func addFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(full, content); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	run(t, dir, "add", "-A")
	c := []string{"commit", "-q", "--allow-empty", "-m", msg}
	run(t, dir, c...)
}

func kinds(ds []Drift) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Kind)
	}
	return out
}

func hasKind(ds []Drift, kind string) bool {
	for _, d := range ds {
		if d.Kind == kind {
			return true
		}
	}
	return false
}

func detect(t *testing.T, dir string) []Drift {
	t.Helper()
	got, err := DetectDrift(dir, "main")
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}
	return got
}

const baseGoMod = "module example.com/x\n\ngo 1.26.6\n"

func TestRequiredModules(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  string
		want []string
	}{
		{"none", baseGoMod, nil},
		{"single line", "module m\n\nrequire example.com/a v1.0.0\n", []string{"example.com/a"}},
		{
			"block",
			"module m\n\nrequire (\n\texample.com/a v1.0.0\n\texample.com/b v2.0.0\n)\n",
			[]string{"example.com/a", "example.com/b"},
		},
		{
			"indirect does not count",
			"module m\n\nrequire (\n\texample.com/a v1.0.0\n\texample.com/b v2.0.0 // indirect\n)\n",
			[]string{"example.com/a"},
		},
		{
			"replace and toolchain are not requirements",
			"module m\n\ngo 1.26.6\n\ntoolchain go1.26.6\n\nreplace (\n\texample.com/a => ../a\n)\n\nexclude example.com/c v1.0.0\n",
			nil,
		},
		{
			"trailing comment is not part of the path",
			"module m\n\nrequire example.com/a v1.0.0 // because\n",
			[]string{"example.com/a"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := requiredModules(tt.src)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for _, w := range tt.want {
				if !got[w] {
					t.Errorf("missing %q in %v", w, got)
				}
			}
		})
	}
}

func TestNewDependencyIsFlagged(t *testing.T) {
	dir := driftRepo(t, map[string]string{"go.mod": baseGoMod})
	addFile(t, dir, "go.mod", "module example.com/x\n\ngo 1.26.6\n\nrequire example.com/dep v1.2.3\n")
	commitAll(t, dir, "add a dependency")

	got := detect(t, dir)
	if !hasKind(got, "new-dependency") {
		t.Fatalf("new dependency not flagged: %v", kinds(got))
	}
	for _, d := range got {
		if d.Kind == "new-dependency" && !strings.Contains(d.Detail, "example.com/dep") {
			t.Errorf("flag does not name the module: %q", d.Detail)
		}
	}
}

func TestVersionBumpIsNotADecision(t *testing.T) {
	dir := driftRepo(t, map[string]string{
		"go.mod": "module example.com/x\n\ngo 1.26.6\n\nrequire example.com/dep v1.0.0\n",
	})
	addFile(t, dir, "go.mod", "module example.com/x\n\ngo 1.26.6\n\nrequire example.com/dep v1.2.3\n")
	commitAll(t, dir, "bump dep")

	if got := detect(t, dir); hasKind(got, "new-dependency") {
		t.Fatalf("a version bump was reported as a new dependency: %+v", got)
	}
}

func TestRemovingADependencyIsNotFlagged(t *testing.T) {
	dir := driftRepo(t, map[string]string{
		"go.mod": "module example.com/x\n\ngo 1.26.6\n\nrequire example.com/dep v1.0.0\n",
	})
	addFile(t, dir, "go.mod", baseGoMod)
	commitAll(t, dir, "drop dep")

	if got := detect(t, dir); hasKind(got, "new-dependency") {
		t.Fatalf("a removal was reported as a new dependency: %+v", got)
	}
}

// ---- suppression ----------------------------------------------------------

func TestAnADRSuppressesEveryFlag(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(t *testing.T, dir string)
	}{
		{"an ADR file is added", func(t *testing.T, dir string) {
			addFile(t, dir, "docs/adr/0002-we-decided.md", "# 0002. We decided\n")
			commitAll(t, dir, "add the dependency")
		}},
		{"a commit message cites one", func(t *testing.T, dir string) {
			commitAll(t, dir, "add the dependency, per ADR 0002")
		}},
		{"an added line cites one", func(t *testing.T, dir string) {
			addFile(t, dir, "README.md", "See ADR-0002 for why.\n")
			commitAll(t, dir, "add the dependency")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := driftRepo(t, map[string]string{"go.mod": baseGoMod})
			addFile(t, dir, "go.mod", "module example.com/x\n\ngo 1.26.6\n\nrequire example.com/dep v1.2.3\n")
			tt.mutate(t, dir)

			if got := detect(t, dir); len(got) != 0 {
				t.Fatalf("branch recorded a decision but was still flagged: %+v", got)
			}
		})
	}
}

func TestAContextLineDoesNotSuppress(t *testing.T) {
	// The base file already mentions an ADR. That is the *previous* decision
	// speaking, not this branch, so it must not buy the branch silence.
	dir := driftRepo(t, map[string]string{
		"go.mod":    baseGoMod,
		"README.md": "line one\nSee ADR 0003 for the old decision.\nline three\n",
	})
	addFile(t, dir, "README.md", "line one CHANGED\nSee ADR 0003 for the old decision.\nline three\n")
	addFile(t, dir, "go.mod", "module example.com/x\n\ngo 1.26.6\n\nrequire example.com/dep v1.2.3\n")
	commitAll(t, dir, "add a dependency")

	if got := detect(t, dir); !hasKind(got, "new-dependency") {
		t.Fatalf("a pre-existing ADR mention suppressed the check: %v", kinds(got))
	}
}

// ---- public-interface -----------------------------------------------------

func TestExportedSigs(t *testing.T) {
	t.Run("package main yields nothing", func(t *testing.T) {
		got, err := exportedSigs([]byte("package main\n\nfunc Run(a int) error { return nil }\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("package main produced signatures: %v", got)
		}
	})

	t.Run("unexported declarations are skipped", func(t *testing.T) {
		got, err := exportedSigs([]byte("package p\n\nfunc run(a int) {}\ntype thing struct{}\n"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("unexported declarations leaked: %v", got)
		}
	})

	for _, tt := range []struct {
		name    string
		a, b    string
		key     string
		differs bool
	}{
		{
			"renaming a parameter is not a change",
			"package p\nfunc F(a, b int) error { return nil }\n",
			"package p\nfunc F(x, y int) error { return nil }\n",
			"F", false,
		},
		{
			"moving a declaration down the file is not a change",
			"package p\nfunc F(a int) error { return nil }\n",
			"package p\n\n// a new comment\n//\n// and more of it\n\nfunc F(a int) error { return nil }\n",
			"F", false,
		},
		{
			"changing a parameter type is a change",
			"package p\nfunc F(a int) error { return nil }\n",
			"package p\nfunc F(a string) error { return nil }\n",
			"F", true,
		},
		{
			"collapsing two parameters into one is a change",
			"package p\nfunc F(a, b int) error { return nil }\n",
			"package p\nfunc F(a int) error { return nil }\n",
			"F", true,
		},
		{
			"adding a result is a change",
			"package p\nfunc F() error { return nil }\n",
			"package p\nfunc F() (int, error) { return 0, nil }\n",
			"F", true,
		},
		{
			"a struct gaining a field is not a change",
			"package p\ntype T struct{ A int }\n",
			"package p\ntype T struct {\n\tA int\n\tB string\n}\n",
			"T", false,
		},
		{
			"a struct becoming an alias is a change",
			"package p\ntype T struct{ A int }\n",
			"package p\ntype T = int\n",
			"T", true,
		},
		{
			"a method keyed by its receiver",
			"package p\ntype T struct{}\n\nfunc (t T) Do(a int) {}\n",
			"package p\ntype T struct{}\n\nfunc (t T) Do(a string) {}\n",
			"(T).Do", true,
		},
		{
			"a pointer receiver is part of the signature",
			"package p\ntype T struct{}\n\nfunc (t T) Do() {}\n",
			"package p\ntype T struct{}\n\nfunc (t *T) Do() {}\n",
			"(T).Do", true,
		},
		{
			"a constant's value is not its signature",
			"package p\nconst Budget = 2\n",
			"package p\nconst Budget = 3\n",
			"Budget", false,
		},
		{
			"a constant's type is",
			"package p\nconst Budget int = 2\n",
			"package p\nconst Budget int64 = 2\n",
			"Budget", true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, err := exportedSigs([]byte(tt.a))
			if err != nil {
				t.Fatal(err)
			}
			b, err := exportedSigs([]byte(tt.b))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := a[tt.key]; !ok {
				t.Fatalf("key %q missing from %v", tt.key, a)
			}
			if got := a[tt.key] != b[tt.key]; got != tt.differs {
				t.Errorf("differs = %v, want %v\n  a: %q\n  b: %q", got, tt.differs, a[tt.key], b[tt.key])
			}
		})
	}
}

func TestChangedInterfaceIsFlagged(t *testing.T) {
	dir := driftRepo(t, map[string]string{
		"go.mod":     baseGoMod,
		"pkg/api.go": "package pkg\n\nfunc Serve(addr string) error { return nil }\n",
	})
	addFile(t, dir, "pkg/api.go", "package pkg\n\nfunc Serve(addr string, tls bool) error { return nil }\n")
	commitAll(t, dir, "take a tls flag")

	got := detect(t, dir)
	if !hasKind(got, "public-interface") {
		t.Fatalf("a changed exported signature was not flagged: %v", kinds(got))
	}
}

func TestInterfaceAdditionsAndMainAreNotFlagged(t *testing.T) {
	for _, tt := range []struct {
		name       string
		file, from string
		to         string
	}{
		{
			"a new exported function is not a re-derivable decision",
			"pkg/api.go",
			"package pkg\n\nfunc Serve(addr string) error { return nil }\n",
			"package pkg\n\nfunc Serve(addr string) error { return nil }\n\nfunc Stop() {}\n",
		},
		{
			"package main has no public interface to break",
			"cmd/main.go",
			"package main\n\nfunc Serve(addr string) error { return nil }\n",
			"package main\n\nfunc Serve(addr string, tls bool) error { return nil }\n",
		},
		{
			"a test file is not public surface",
			"pkg/api_test.go",
			"package pkg\n\nfunc Helper(a int) {}\n",
			"package pkg\n\nfunc Helper(a string) {}\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := driftRepo(t, map[string]string{"go.mod": baseGoMod, tt.file: tt.from})
			addFile(t, dir, tt.file, tt.to)
			commitAll(t, dir, "change it")

			if got := detect(t, dir); hasKind(got, "public-interface") {
				t.Fatalf("flagged something that is not an interface break: %+v", got)
			}
		})
	}
}

func TestDeclarationMovedBetweenFilesIsNotARemoval(t *testing.T) {
	// A refactor that shuffles declarations inside one package changes no
	// interface. Keying per file rather than per package would report this as a
	// removal, which is exactly the kind of false positive that gets the whole
	// check deleted.
	dir := driftRepo(t, map[string]string{
		"go.mod":     baseGoMod,
		"pkg/a.go":   "package pkg\n\nfunc Serve(addr string) error { return nil }\n",
		"pkg/b.go":   "package pkg\n\nfunc Stop() {}\n",
		"README.md":  "hello\n",
		"pkg/doc.go": "package pkg\n",
	})
	addFile(t, dir, "pkg/a.go", "package pkg\n")
	addFile(t, dir, "pkg/b.go", "package pkg\n\nfunc Stop() {}\n\nfunc Serve(addr string) error { return nil }\n")
	commitAll(t, dir, "move Serve into b")

	if got := detect(t, dir); hasKind(got, "public-interface") {
		t.Fatalf("a move within one package was reported as an interface change: %+v", got)
	}
}

// ---- covered-area ---------------------------------------------------------

const adrNamingFleet = "# 0005. Where tooling lives\n\nFleet tooling lives in `tools/fleet`.\n" +
	"Per-project tooling lives in `tools/dora`, which is not in this repo.\n" +
	"Invoke it with `go run ...@latest`.\n"

func TestAdrPathsKeepsOnlyPathsThatExist(t *testing.T) {
	dir := driftRepo(t, map[string]string{
		"docs/adr/0005-where.md": adrNamingFleet,
		"tools/fleet/main.go":    "package main\n\nfunc main() {}\n",
	})
	commitAll(t, dir, "no-op")

	got, err := adrPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	paths := got["docs/adr/0005-where.md"]
	want := map[string]bool{"tools/fleet": true}
	for _, p := range paths {
		if !want[p] {
			t.Errorf("kept a token that is not a path in this repo: %q (all: %v)", p, paths)
		}
	}
	if len(paths) != 1 {
		t.Fatalf("got %v, want exactly [tools/fleet]", paths)
	}
}

func TestCoveredAreaIsFlaggedAndNamesItsADR(t *testing.T) {
	dir := driftRepo(t, map[string]string{
		"docs/adr/0005-where.md": adrNamingFleet,
		"tools/fleet/main.go":    "package main\n\nfunc main() {}\n",
		"go.mod":                 baseGoMod,
	})
	addFile(t, dir, "tools/fleet/main.go", "package main\n\nfunc main() { println(\"hi\") }\n")
	commitAll(t, dir, "touch the fleet")

	got := detect(t, dir)
	var found *Drift
	for i := range got {
		if got[i].Kind == "covered-area" {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatalf("a change under a path ADR 0005 names was not flagged: %v", kinds(got))
	}
	if found.ADR != "docs/adr/0005-where.md" {
		t.Errorf("flag does not name its ADR: %+v", *found)
	}
	if found.Path != "tools/fleet" {
		t.Errorf("flag does not name the covered path: %+v", *found)
	}
}

func TestUncoveredChangeIsSilent(t *testing.T) {
	dir := driftRepo(t, map[string]string{
		"docs/adr/0005-where.md": adrNamingFleet,
		"tools/fleet/main.go":    "package main\n\nfunc main() {}\n",
		"unrelated/thing.txt":    "a\n",
		"go.mod":                 baseGoMod,
	})
	addFile(t, dir, "unrelated/thing.txt", "b\n")
	commitAll(t, dir, "change something nobody decided about")

	if got := detect(t, dir); len(got) != 0 {
		t.Fatalf("an uncovered change was flagged: %+v", got)
	}
}

// ---- the whole thing ------------------------------------------------------

func TestCleanBranchFlagsNothing(t *testing.T) {
	dir := driftRepo(t, map[string]string{
		"go.mod":     baseGoMod,
		"pkg/api.go": "package pkg\n\nfunc Serve(addr string) error { return nil }\n",
	})
	addFile(t, dir, "pkg/api.go", "package pkg\n\n// Serve starts the server.\nfunc Serve(addr string) error { return nil }\n")
	commitAll(t, dir, "document Serve")

	if got := detect(t, dir); len(got) != 0 {
		t.Fatalf("a comment-only change was flagged: %+v", got)
	}
}

func TestMissingBaseRefIsAnErrorNotAPanic(t *testing.T) {
	dir := driftRepo(t, map[string]string{"go.mod": baseGoMod})
	commitAll(t, dir, "work")

	if _, err := DetectDrift(dir, "origin/nonexistent"); err == nil {
		t.Fatal("an unresolvable base ref was accepted")
	}
}
