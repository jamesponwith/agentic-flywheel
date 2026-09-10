// The repo-scoped coordinator lock (fw-kam).
//
// repoSlot (run.go) makes every builder solo in its own repo, but only inside
// ONE process. nightly.sh's quota-hold resume path schedules a uniquely-named
// transient systemd-run unit by design, meant to chain with the next scheduled
// run — so a resume and a manual `fleet run -execute` can overlap as two
// independent coordinator processes, both touching the same repo's worktree at
// once. This closes that gap: a builder is dispatched only after it holds this
// repo's lock, for as long as it is actually working.
//
// This is an flock(2) advisory lock, not a PID file. A PID-file-plus-
// liveness-check version was written first and thrown out on review: a dead
// PID can be recycled by an unrelated live process, and a `kill(pid, 0)`
// permission error (a live process owned by a different user) has no safe
// default — treat it as dead and two coordinators can run at once, which is
// the exact race this exists to close; treat it as alive and a genuinely
// released lock looks permanently stuck. flock has neither failure mode: the
// kernel ties the lock to the open file description and releases it the
// instant that closes, cleanly or via SIGKILL, with no PID bookkeeping.
//
// ponytail: flock is per-host, like the PID file it replaced — it cannot see
// a coordinator running on a different machine. Every caller today is one
// machine (a systemd user timer and a human's terminal), so a cross-host
// protocol is not worth building yet.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const coordinatorLockFile = "coordinator.lock"

func coordinatorLockPath(repoPath string) string {
	return filepath.Join(repoPath, ".flywheel", coordinatorLockFile)
}

// acquireCoordinatorLock claims exclusive dispatch rights to repoPath, for
// whichever bead this call is about to build. It refuses outright rather than
// waiting: a coordinator that blocked here would hold its goroutine budget
// hostage for a repo another process already owns, and the caller has a
// perfectly good move when refused — skip this assignment, the way a kill
// switch or a quota hold already does.
func acquireCoordinatorLock(repoPath, bead string) (release func(), err error) {
	p := coordinatorLockPath(repoPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%s is held by another fleet run — not dispatching to this repo", p)
		}
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	// Diagnostics only, for a human reading the file while it is held — never
	// read back to decide anything, so a partial write here (disk full,
	// killed mid-write) can never wedge a future acquire.
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "%d\n%s dispatching %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339), bead)
	return func() { _ = f.Close() }, nil // Close() releases the flock (flock(2)).
}
