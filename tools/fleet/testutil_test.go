package main

import (
	"os"
	"os/exec"
)

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// gitCmd is the only way a test in this package should build a git subprocess.
// It goes through inDir, so a test's scratch repo cannot be the repository the
// test is running inside — see gitenv.go for why that needed saying.
func gitCmd(dir string, args ...string) *exec.Cmd {
	return inDir(dir, "git", args...)
}
