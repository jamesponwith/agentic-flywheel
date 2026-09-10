package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The race fw-kam is about: two independent OS processes — the nightly timer
// and a quota-hold resume it chained, or a human's manual -execute — must not
// both be allowed to dispatch a builder to the same repo at once.
//
// flock(2) locks are scoped to the open file description, not the process:
// two independent os.OpenFile calls on the same path conflict with each other
// exactly as they would from two separate processes, whether or not they
// happen to run inside the same test binary. acquireCoordinatorLock always
// opens its own file description, so calling it twice here exercises the
// identical kernel path a second `fleet run` process would hit.
func TestCoordinatorLockRefusesALiveHolder(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireCoordinatorLock(dir, "fw-holder")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	if _, err := acquireCoordinatorLock(dir, "fw-x"); err == nil {
		t.Fatal("a second coordinator was allowed to dispatch while the first still holds the lock")
	}
}

// A coordinator killed mid-build (SIGKILL from the run ceiling, a crashed
// machine) must not wedge the repo forever. flock releases the instant the
// holder's file descriptor closes — cleanly here via release(), and
// identically at the kernel level when a killed process's fd table is torn
// down — so the next acquire must succeed without anyone cleaning up a file.
func TestCoordinatorLockReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireCoordinatorLock(dir, "fw-x")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()

	release2, err := acquireCoordinatorLock(dir, "fw-y")
	if err != nil {
		t.Fatalf("a released lock refused a fresh acquire: %v", err)
	}
	release2()
}

// The property Run()'s goroutines actually depend on: however many
// coordinators race for the same repo, never more than one is inside the
// locked section at the same instant. A test that only checked "somebody
// eventually gets in" would pass even if flock excluded nobody.
func TestCoordinatorLockOnlyOneHolderAtAnyInstant(t *testing.T) {
	dir := t.TempDir()
	const attempts = 20
	var mu sync.Mutex
	holders, maxHolders := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := acquireCoordinatorLock(dir, "fw-x")
			if err != nil {
				return
			}
			defer release()
			mu.Lock()
			holders++
			if holders > maxHolders {
				maxHolders = holders
			}
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			holders--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if maxHolders > 1 {
		t.Fatalf("%d goroutines held the coordinator lock at the same instant — flock did not exclude them", maxHolders)
	}
}

func TestCoordinatorLockPath(t *testing.T) {
	if got, want := coordinatorLockPath("/repo"), filepath.Join("/repo", ".flywheel", "coordinator.lock"); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestCoordinatorLockCreatesTheFlywheelDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-yet-created")
	release, err := acquireCoordinatorLock(dir, "fw-x")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	if _, err := os.Stat(coordinatorLockPath(dir)); err != nil {
		t.Errorf("lock file was not created: %v", err)
	}
}
