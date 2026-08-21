// A git subprocess takes its repository from the environment before it takes it
// from its working directory. GIT_DIR and its relatives are absolute paths and
// outrank cmd.Dir, and git exports them to every hook it runs — so anything
// launched from inside a hook acts on the repository git was already working in,
// whatever directory it was pointed at.
//
// That is not theoretical here. lefthook runs `go test -short ./...` from
// pre-commit, and one such run moved local main onto a chain of test-fixture
// commits, created the branches the fixtures asked for, and added a commit to
// the branch being committed. Every test passed, because from inside the test
// every command did succeed — on the wrong repository (fw-u6g).
//
// Every subprocess in this package therefore goes through inDir, which strips
// the pointers and leaves cmd.Dir as the only thing that decides.
package main

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// repoPointers name a repository, an index, or an object store absolutely.
var repoPointers = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_PREFIX",
	"GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_NAMESPACE",
}

// hermeticEnv is the caller's environment minus every repository pointer.
func hermeticEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if slices.Contains(repoPointers, k) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// inDir builds a subprocess that acts on dir and nowhere else. Use it for git,
// and for anything that is a git client underneath — bd keeps its issues in one.
func inDir(dir, name string, args ...string) *exec.Cmd {
	return inDirContext(context.Background(), dir, name, args...)
}

// inDirContext is inDir with a deadline. Our own subprocesses return; a repo's
// git hook is somebody else's code and might not, and a doctor that hangs on
// one repo never reaches the rest of the roster.
func inDirContext(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Env = hermeticEnv()
	return c
}
