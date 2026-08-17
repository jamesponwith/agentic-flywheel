// Tests for guard.sh's audit log and review ledger.
//
// This is a Go test around a shell script because the repo already runs
// `go test -short ./...` in the pre-commit gate and in CI, so the refusals
// below get exercised on every commit without a second test runner. It needs
// only bash and git, so it stays in the fast lane rather than behind the
// conformance tag.
package flywheel

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// leakedEnv are the variables that would make a subprocess act on a repository
// or as an identity the test did not choose. git exports the GIT_* ones to its
// own hooks, and lefthook runs `go test` from the pre-commit hook — so without
// this, "scratch" is the real repo and "unset agent" is whoever is committing
// (fw-u6g).
var leakedEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_PREFIX", "GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE",
	"FLYWHEEL_AGENT", "FLYWHEEL_HOME",
}

func hermeticEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if slices.Contains(leakedEnv, k) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// scratch returns an empty git repo for guard.sh to treat as its own, so a
// test never appends to the real .flywheel/agent-log.jsonl.
func scratch(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	c := exec.Command("git", "-C", dir, "init", "-q")
	c.Env = hermeticEnv()
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}

func guardPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("guard.sh")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// runGuard invokes guard.sh inside dir and returns stderr and the exit code.
func runGuard(t *testing.T, dir, agent string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(guardPath(t), args...)
	cmd.Dir = dir
	// Strip any inherited FLYWHEEL_AGENT before deciding. Appending only when
	// agent != "" does NOT mean unset — it means "whatever the parent had",
	// so a test asserting the unset behaviour passed for the wrong reason
	// whenever this suite ran from inside a builder (fw-kdu).
	env := make([]string, 0, len(hermeticEnv())+2)
	for _, kv := range hermeticEnv() {
		if !strings.HasPrefix(kv, "FLYWHEEL_AGENT=") {
			env = append(env, kv)
		}
	}
	env = append(env, "FLYWHEEL_HOME="+filepath.Join(dir, "home"))
	if agent != "" {
		env = append(env, "FLYWHEEL_AGENT="+agent)
	}
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running guard.sh: %v", err)
	}
	return stderr.String(), code
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// jsonTopKeys returns one object's keys in order, duplicates included.
// encoding/json's map decoding is last-wins, which is exactly the behaviour
// that hid this bug, so the test reads the token stream instead.
func jsonTopKeys(t *testing.T, line string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not a JSON object: %q (%v)", line, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("reading key in %q: %v", line, err)
		}
		k, ok := tok.(string)
		if !ok {
			t.Fatalf("non-string key %v in %q", tok, line)
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("reading value for %q in %q: %v", k, line, err)
		}
		keys = append(keys, k)
	}
	return keys
}

// A pair guard.sh must refuse writes nothing at all. Refusing after appending
// half a record would corrupt the log the refusal exists to protect.
func TestRefusesBadPairs(t *testing.T) {
	tests := []struct {
		name    string
		cmd     []string
		file    string
		wantErr string
	}{
		{"log reserved agent", []string{"log", "e", "agent=x"}, "agent-log.jsonl", "set FLYWHEEL_AGENT instead"},
		{"log reserved ts", []string{"log", "e", "ts=x"}, "agent-log.jsonl", "written automatically"},
		{"log reserved event", []string{"log", "e", "event=x"}, "agent-log.jsonl", "written automatically"},
		{"log reserved repo", []string{"log", "e", "repo=x"}, "agent-log.jsonl", "written automatically"},
		{"log reserved after a good pair", []string{"log", "e", "bead=fw-1", "agent=x"}, "agent-log.jsonl", "written automatically"},
		{"log missing equals", []string{"log", "e", "bead"}, "agent-log.jsonl", "is not key=value"},
		{"log empty key", []string{"log", "e", "=v"}, "agent-log.jsonl", "empty key"},
		{"finding reserved commit", []string{"finding", "commit=x"}, "review.jsonl", "written automatically"},
		{"finding reserved branch", []string{"finding", "branch=x"}, "review.jsonl", "written automatically"},
		{"finding missing equals", []string{"finding", "lens"}, "review.jsonl", "is not key=value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := scratch(t)
			stderr, code := runGuard(t, dir, "test/agent", tt.cmd...)
			if code == 0 {
				t.Fatalf("accepted %v; want non-zero exit", tt.cmd)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr = %q, want it to mention %q", stderr, tt.wantErr)
			}
			if got := readLines(t, filepath.Join(dir, ".flywheel", tt.file)); len(got) != 0 {
				t.Errorf("refusal still wrote %d line(s): %q", len(got), got)
			}
		})
	}
}

// The bug in fw-7mw: two "agent" keys in one record, because the caller passed
// what the record already writes for itself.
func TestLogWritesEachFieldOnce(t *testing.T) {
	dir := scratch(t)
	if _, code := runGuard(t, dir, "fleet/builder-go", "log", "bead.pr_opened", "bead=fw-ldz", "pr=19"); code != 0 {
		t.Fatalf("exit %d; want 0", code)
	}
	lines := readLines(t, filepath.Join(dir, ".flywheel", "agent-log.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
	}

	seen := map[string]int{}
	for _, k := range jsonTopKeys(t, lines[0]) {
		seen[k]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("key %q appears %d times; every field must appear exactly once", k, n)
		}
	}
	for _, k := range []string{"ts", "event", "agent", "repo", "bead", "pr"} {
		if seen[k] == 0 {
			t.Errorf("missing key %q in %s", k, lines[0])
		}
	}

	var rec map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unparseable record %q: %v", lines[0], err)
	}
	if rec["agent"] != "fleet/builder-go" {
		t.Errorf("agent = %q, want the value of $FLYWHEEL_AGENT", rec["agent"])
	}
	if rec["event"] != "bead.pr_opened" || rec["bead"] != "fw-ldz" {
		t.Errorf("record = %v", rec)
	}
}

func TestUnsetAgentIsRecordedAsUnknown(t *testing.T) {
	dir := scratch(t)
	if _, code := runGuard(t, dir, "", "log", "gate.skipped", "reason=rebase"); code != 0 {
		t.Fatalf("exit %d; want 0", code)
	}
	var rec map[string]string
	lines := readLines(t, filepath.Join(dir, ".flywheel", "agent-log.jsonl"))
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["agent"] != "unknown" {
		t.Errorf("agent = %q, want %q", rec["agent"], "unknown")
	}
}

// The regression the helper hid: with FLYWHEEL_AGENT set in the environment,
// asking for the unset case must still get the unset case.
func TestUnsetAgentIgnoresAnInheritedValue(t *testing.T) {
	t.Setenv("FLYWHEEL_AGENT", "inherited/impostor")
	dir := scratch(t)
	if _, code := runGuard(t, dir, "", "log", "probe"); code != 0 {
		t.Fatalf("exit %d; want 0", code)
	}
	var rec map[string]string
	lines := readLines(t, filepath.Join(dir, ".flywheel", "agent-log.jsonl"))
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["agent"] == "inherited/impostor" {
		t.Error("an inherited FLYWHEEL_AGENT leaked into a test asking for unset")
	}
}

// Keys are escaped like values. An unescaped one would break the record just as
// thoroughly as a duplicate, and for the same reason: the caller controls it.
func TestEscapesKeysAndValues(t *testing.T) {
	dir := scratch(t)
	weird := "a\"b\\c\td\re"
	if _, code := runGuard(t, dir, "test/agent", "log", "e", `qu"ote=`+weird); code != 0 {
		t.Fatalf("exit %d; want 0", code)
	}
	lines := readLines(t, filepath.Join(dir, ".flywheel", "agent-log.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	var rec map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unparseable record %q: %v", lines[0], err)
	}
	if rec[`qu"ote`] != weird {
		t.Errorf("value = %q, want %q", rec[`qu"ote`], weird)
	}
}

// Appending is the only write. Two runs leave two records, in order.
func TestLogAppends(t *testing.T) {
	dir := scratch(t)
	for _, bead := range []string{"fw-1", "fw-2"} {
		if _, code := runGuard(t, dir, "test/agent", "log", "bead.claimed", "bead="+bead); code != 0 {
			t.Fatalf("exit %d; want 0", code)
		}
	}
	lines := readLines(t, filepath.Join(dir, ".flywheel", "agent-log.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "fw-1") || !strings.Contains(lines[1], "fw-2") {
		t.Errorf("records out of order: %q", lines)
	}
}
