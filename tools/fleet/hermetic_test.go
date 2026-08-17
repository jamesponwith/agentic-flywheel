package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hermeticHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := gitCmd(dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse in %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}

// The whole point of a scratch repo is that a test cannot reach the repository
// it is running inside. cmd.Dir does not achieve that: GIT_DIR is absolute and
// outranks it, git exports GIT_DIR to its own hooks, and lefthook runs
// `go test -short ./...` from the pre-commit hook. So every helper here used to
// operate on whatever repository was being committed to.
//
// It is not a near miss. One pre-commit run moved local main onto a chain of
// fixture commits, created `feature` and `work` because fixtures asked for
// them, and added a commit to the branch under commit — while every test
// reported success, because from inside the test everything did succeed.
func TestScratchReposIgnoreAnInheritedGitDir(t *testing.T) {
	decoy := gitRepo(t)
	commit(t, decoy, "the decoy's only commit")
	before := hermeticHead(t, decoy)

	t.Setenv("GIT_DIR", filepath.Join(decoy, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(decoy, ".git", "index"))

	scratch := gitRepo(t)
	commit(t, scratch, "work that belongs to the scratch repo")
	run(t, scratch, "checkout", "-q", "-b", "feature")
	commit(t, scratch, "more scratch work")

	if got := hermeticHead(t, decoy); got != before {
		t.Errorf("the inherited GIT_DIR won: decoy HEAD moved %s -> %s", before, got)
	}
	if _, err := os.Stat(filepath.Join(decoy, ".git", "refs", "heads", "feature")); err == nil {
		t.Error("a scratch branch was created in the decoy repository")
	}
	if hermeticHead(t, scratch) == before {
		t.Error("the scratch repo and the decoy share a HEAD — they are the same repo")
	}
}

// Every variable that names a repository has to go, not just GIT_DIR. A test
// that cleared one and inherited the rest would look fixed and not be.
func TestHermeticEnvDropsEveryRepositoryPointer(t *testing.T) {
	for _, k := range repoPointers {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, "/somewhere/else")
			for _, kv := range hermeticEnv() {
				if name, _, _ := strings.Cut(kv, "="); name == k {
					t.Errorf("%s survived into the subprocess environment", k)
				}
			}
		})
	}
}
