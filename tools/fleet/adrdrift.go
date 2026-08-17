// ADR drift detection. The Intent stage exists to stop a decision being
// re-derived six weeks later because nobody wrote down why, and the only
// evidence that it is working is a branch that made a decision and recorded it.
// This check looks for the other case: a branch that made one and did not.
//
// Three signals, all recoverable from git alone, all advisory:
//
//	new-dependency    go.mod requires a module the base did not
//	public-interface  an exported top-level decl changed shape or disappeared
//	covered-area      a changed file sits under a path an existing ADR names
//
// One suppression rule covers all three: if the branch touches docs/adr/, or
// says "ADR NNNN" anywhere in its commit messages or its added lines, it is
// silent. That is deliberately generous. The acceptance criterion deletes this
// check if it is wrong more than half the time, so every ambiguity resolves
// toward saying nothing — false negatives are cheap here, false positives are
// fatal.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strings"
)

type Drift struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
	ADR    string `json:"adr,omitempty"`
}

// DetectDrift compares HEAD against its merge base with `base` and reports
// decisions that look unrecorded. A suppressed branch returns no drift and no
// error: silence is the answer, not a status.
func DetectDrift(dir, base string) ([]Drift, error) {
	suppressed, err := referencesADR(dir, base)
	if err != nil {
		return nil, err
	}
	if suppressed {
		return nil, nil
	}
	mb, err := mergeBase(dir, base)
	if err != nil {
		return nil, err
	}
	changed, err := changedFiles(dir, mb)
	if err != nil {
		return nil, err
	}

	var out []Drift
	out = append(out, newDependencies(dir, mb)...)
	out = append(out, interfaceChanges(dir, mb, changed)...)
	covered, err := coveredAreas(dir, changed)
	if err != nil {
		return nil, err
	}
	out = append(out, covered...)

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Detail < b.Detail
	})
	return out, nil
}

// adrMention is what "this branch already recorded a decision" looks like in
// text: a reference to an ADR by number, or to the directory they live in.
var adrMention = regexp.MustCompile(`(?i)(adr[ _-]?\d{4}|docs/adr/)`)

// referencesADR reports whether the branch accompanies its changes with an ADR
// or a reference to one. Only *added* diff lines count — a context line that
// happens to sit near an ADR mention says nothing about this branch.
//
// ponytail: the match is global and textual, so it cannot tell whether the ADR
// is about the thing flagged, and any added line that merely contains the
// string buys the whole branch silence. This file suppresses itself for exactly
// that reason. Tightening it — per-signal suppression, or matching an ADR to
// the paths it names — is the upgrade to make once there is calibration data
// saying the check is too quiet rather than too loud.
func referencesADR(dir, base string) (bool, error) {
	mb, err := mergeBase(dir, base)
	if err != nil {
		return false, err
	}
	changed, err := changedFiles(dir, mb)
	if err != nil {
		return false, err
	}
	for _, f := range changed {
		if strings.HasPrefix(f, "docs/adr/") {
			return true, nil
		}
	}
	msgs, err := git(dir, "log", "--format=%B", mb+"..HEAD")
	if err != nil {
		return false, err
	}
	if adrMention.MatchString(msgs) {
		return true, nil
	}
	// --unified=0 so there are no context lines to mistake for the branch's own
	// words. +++ is the file header, not content.
	diff, err := git(dir, "diff", "--unified=0", mb, "HEAD")
	if err != nil {
		return false, err
	}
	for _, line := range lines(diff) {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		if adrMention.MatchString(line) {
			return true, nil
		}
	}
	return false, nil
}

// ---- new-dependency -------------------------------------------------------

func newDependencies(dir, mb string) []Drift {
	baseSrc, err := git(dir, "show", mb+":go.mod")
	if err != nil {
		return nil // no manifest at the base: nothing to have added to
	}
	headSrc, err := git(dir, "show", "HEAD:go.mod")
	if err != nil {
		return nil
	}
	was := requiredModules(baseSrc)
	var added []string
	for m := range requiredModules(headSrc) {
		if !was[m] {
			added = append(added, m)
		}
	}
	sort.Strings(added)
	var out []Drift
	for _, m := range added {
		out = append(out, Drift{
			Kind: "new-dependency", Path: "go.mod",
			Detail: "requires " + m + ", which the base did not — CLAUDE.md wants an ADR for that",
		})
	}
	return out
}

// requiredModules reads the *direct* requirements out of a go.mod. Hand-parsed
// rather than via golang.org/x/mod, which would itself be the first new
// dependency this check exists to notice.
//
// Version bumps do not count (the module is already required), and neither do
// removals, replace/exclude/retract blocks, or the go and toolchain directives.
// An "// indirect" line is a transitive edge nobody chose, so it is not a
// decision either.
//
// ponytail: go.mod is the only manifest read, because this repo is stdlib-only
// Go. A package.json or pyproject.toml would each need their own reader.
func requiredModules(src string) map[string]bool {
	out := map[string]bool{}
	inRequireBlock := false
	for _, line := range lines(src) {
		if i := strings.Index(line, "//"); i >= 0 {
			if strings.Contains(line[i:], "indirect") {
				continue
			}
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case line == "require (":
			inRequireBlock = true
		case inRequireBlock && line == ")":
			inRequireBlock = false
		case strings.HasPrefix(line, "require "):
			if f := strings.Fields(line); len(f) >= 2 {
				out[f[1]] = true
			}
		case inRequireBlock:
			if f := strings.Fields(line); len(f) >= 1 {
				out[f[0]] = true
			}
		}
	}
	return out
}

// ---- public-interface -----------------------------------------------------

// interfaceChanges reports exported declarations that changed shape or vanished.
// Additions are not reported: adding to an interface is not a decision anyone
// will have to re-derive.
//
// Keys are scoped to the package directory rather than the file, so a
// declaration moved between two files of the same package — or a file renamed
// wholesale — reads as unchanged rather than as a removal plus an addition.
func interfaceChanges(dir, mb string, changed []string) []Drift {
	type origin struct{ sig, file string }
	was := map[string]origin{}
	now := map[string]string{}

	for _, p := range changed {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		pkg := path.Dir(p)
		if src, err := git(dir, "show", mb+":"+p); err == nil {
			if sigs, err := exportedSigs([]byte(src)); err == nil {
				for k, v := range sigs {
					was[pkg+"."+k] = origin{v, p}
				}
			}
		}
		if src, err := git(dir, "show", "HEAD:"+p); err == nil {
			if sigs, err := exportedSigs([]byte(src)); err == nil {
				for k, v := range sigs {
					now[pkg+"."+k] = v
				}
			}
		}
	}

	var out []Drift
	for k, before := range was {
		after, still := now[k]
		if still && after == before.sig {
			continue
		}
		detail := before.sig + " was removed"
		if still {
			detail = before.sig + " became " + after
		}
		out = append(out, Drift{Kind: "public-interface", Path: before.file, Detail: detail})
	}
	return out
}

// exportedSigs maps each exported top-level declaration to a normalised
// signature. It returns nil for package main: tools/fleet is package main, so
// "exported" there is capitalisation rather than an interface, and flagging it
// would be pure noise.
func exportedSigs(src []byte) (map[string]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "src.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	if f.Name == nil || f.Name.Name == "main" {
		return nil, nil
	}
	sigs := map[string]string{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if key, sig := funcSig(fset, d); key != "" {
				sigs[key] = sig
			}
		case *ast.GenDecl:
			keyword := "var"
			if d.Tok == token.CONST {
				keyword = "const"
			}
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						sigs[s.Name.Name] = typeSig(fset, s)
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if !n.IsExported() {
							continue
						}
						// The value is not part of the signature: re-tuning a
						// constant is not re-deriving a decision, changing its
						// type is.
						sig := keyword + " " + n.Name
						if s.Type != nil {
							sig += " " + norm(printNode(fset, s.Type))
						}
						sigs[n.Name] = sig
					}
				}
			}
		}
	}
	return sigs, nil
}

// funcSig returns a lookup key and a printed signature with parameter names
// stripped, so renaming an argument is not a change. An empty key means the
// declaration is not public surface.
func funcSig(fset *token.FileSet, d *ast.FuncDecl) (key, sig string) {
	if !d.Name.IsExported() {
		return "", ""
	}
	recv := ""
	key = d.Name.Name
	if d.Recv != nil {
		if len(d.Recv.List) != 1 {
			return "", ""
		}
		recv = norm(printNode(fset, d.Recv.List[0].Type))
		base := strings.TrimPrefix(recv, "*")
		if i := strings.IndexByte(base, '['); i >= 0 {
			base = base[:i] // generic receiver: Foo[T] is keyed as Foo
		}
		if !ast.IsExported(base) {
			return "", "" // a method on an unexported type is not reachable
		}
		key = "(" + base + ")." + d.Name.Name
	}

	var b strings.Builder
	b.WriteString("func ")
	if recv != "" {
		b.WriteString("(" + recv + ") ")
	}
	b.WriteString(d.Name.Name)
	b.WriteString(typeParams(fset, d.Type.TypeParams))
	b.WriteString("(" + fieldTypes(fset, d.Type.Params) + ")")
	if r := fieldTypes(fset, d.Type.Results); r != "" {
		b.WriteString(" (" + r + ")")
	}
	return key, b.String()
}

func typeSig(fset *token.FileSet, s *ast.TypeSpec) string {
	sig := "type " + s.Name.Name + typeParams(fset, s.TypeParams)
	if s.Assign.IsValid() {
		sig += " ="
	}
	switch s.Type.(type) {
	// ponytail: only the kind is compared, not the members. A struct that gains
	// a field, or an interface that gains a method, reads as unchanged. Comparing
	// members is the obvious upgrade the first time a real break slips through.
	case *ast.StructType:
		return sig + " struct"
	case *ast.InterfaceType:
		return sig + " interface"
	}
	return sig + " " + norm(printNode(fset, s.Type))
}

// fieldTypes prints a parameter or result list as types only, expanding
// `a, b int` back into two entries so that stripping the names cannot silently
// change the arity.
func fieldTypes(fset *token.FileSet, fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		t := norm(printNode(fset, f.Type))
		n := len(f.Names)
		if n == 0 {
			n = 1 // an unnamed parameter is still one parameter
		}
		for i := 0; i < n; i++ {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, ", ")
}

// typeParams keeps its names, unlike ordinary parameters: a type parameter's
// name is referenced by the rest of the signature.
func typeParams(fset *token.FileSet, fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		var names []string
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
		parts = append(parts, norm(strings.Join(names, ", ")+" "+printNode(fset, f.Type)))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func printNode(fset *token.FileSet, n any) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, n); err != nil {
		return "?"
	}
	return b.String()
}

// norm collapses runs of whitespace to a single space. go/printer decides line
// breaks from node positions, so the same declaration ten lines further down
// would otherwise print differently and read as a change.
func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

// ---- covered-area ---------------------------------------------------------

func coveredAreas(dir string, changed []string) ([]Drift, error) {
	byADR, err := adrPaths(dir)
	if err != nil {
		return nil, err
	}
	owners := map[string][]string{} // covered path -> ADRs naming it
	for adr, paths := range byADR {
		for _, p := range paths {
			owners[p] = append(owners[p], adr)
		}
	}

	type hit struct{ adr, area string }
	files := map[hit][]string{}
	for _, f := range changed {
		if strings.HasPrefix(f, "docs/adr/") {
			continue
		}
		for _, area := range selfAndParents(f) {
			for _, adr := range owners[area] {
				files[hit{adr, area}] = append(files[hit{adr, area}], f)
			}
		}
	}

	var out []Drift
	for h, fs := range files {
		sort.Strings(fs)
		out = append(out, Drift{
			Kind: "covered-area", Path: h.area, ADR: h.adr,
			Detail: fmt.Sprintf("%d changed file(s) here (%s) — %s covers this ground",
				len(fs), strings.Join(fs[:min(3, len(fs))], ", "), path.Base(h.adr)),
		})
	}
	return out, nil
}

var backtickToken = regexp.MustCompile("`([^`\n]+)`")

// adrPaths maps each ADR to the repository paths it names in backticks.
//
// A token counts only if it resolves to a real file or directory at HEAD.
// Existence is the whole filter, so prose, command lines, and paths belonging
// to another repo drop out on their own — `run.go` (ADR 0010) is not a path
// from the root, `tools/dora` (ADR 0005) lives in the template — and the list
// maintains itself as the tree changes.
func adrPaths(dir string) (map[string][]string, error) {
	tracked, err := git(dir, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return nil, err
	}
	exists := map[string]bool{}
	var adrs []string
	for _, f := range lines(tracked) {
		if f == "" {
			continue
		}
		exists[f] = true
		for d := path.Dir(f); d != "." && d != "/"; d = path.Dir(d) {
			exists[d] = true
		}
		if strings.HasPrefix(f, "docs/adr/") && strings.HasSuffix(f, ".md") {
			adrs = append(adrs, f)
		}
	}

	out := map[string][]string{}
	sort.Strings(adrs)
	for _, adr := range adrs {
		src, err := git(dir, "show", "HEAD:"+adr)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, m := range backtickToken.FindAllStringSubmatch(src, -1) {
			t := strings.TrimSpace(m[1])
			t = strings.TrimPrefix(t, "./")
			t = strings.TrimSuffix(t, "/")
			if t == "" || t == "." || seen[t] || !exists[t] {
				continue
			}
			seen[t] = true
			out[adr] = append(out[adr], t)
		}
		sort.Strings(out[adr])
	}
	return out, nil
}

func selfAndParents(f string) []string {
	out := []string{f}
	for d := path.Dir(f); d != "." && d != "/"; d = path.Dir(d) {
		out = append(out, d)
	}
	return out
}

// ---- git ------------------------------------------------------------------

func mergeBase(dir, base string) (string, error) {
	out, err := git(dir, "merge-base", base, "HEAD")
	if err != nil {
		return "", fmt.Errorf("merge-base %s HEAD: %w", base, err)
	}
	mb := strings.TrimSpace(out)
	if mb == "" {
		return "", fmt.Errorf("no merge base between %s and HEAD", base)
	}
	return mb, nil
}

func changedFiles(dir, mb string) ([]string, error) {
	out, err := git(dir, "diff", "--name-only", mb, "HEAD")
	if err != nil {
		return nil, err
	}
	var fs []string
	for _, f := range lines(out) {
		if f != "" {
			fs = append(fs, f)
		}
	}
	return fs, nil
}

// lines splits on newlines rather than on whitespace: a path may contain a
// space, and a diff line certainly does.
func lines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
