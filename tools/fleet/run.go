// run: turn a plan into actual builders.
//
// The coordinator allocates and never edits code (ADR 0006); this is the piece
// that hands each assignment to a builder and gets out of the way. Every
// builder runs the roster's declared agent on "/flywheel-next <bead>" in its
// own worktree, bound by ADR 0003 — branch, commit, gate, PR, and stop. The
// agent is a role, not a product (ADR 0010).
//
// Dry-run is the default. Spawning real agents requires -execute, because a
// scheduler whose safe mode is "do it" is not a safe mode.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Builder is the outcome of one assignment.
type Builder struct {
	Bead     string        `json:"bead"`
	Repo     string        `json:"repo"`
	Agent    string        `json:"agent"`
	Worktree string        `json:"worktree"`
	Started  time.Time     `json:"started"`
	Took     time.Duration `json:"took"`
	Outcome  string        `json:"outcome"` // green | no-op | abandoned | timeout | error | skipped
	Commits  int           `json:"commits"` // evidence: green requires > 0
	Detail   string        `json:"detail"`
}

// RunOpts bounds a night. Every field here exists because something without it
// would be unbounded.
type RunOpts struct {
	Execute    bool          // false = plan only; the default is not to act
	PerBuilder time.Duration // wall-clock cap per builder
	// MaxTurns stops a confused builder circling. It is NOT the real bound —
	// wall-clock is. Run 6 hit 60 turns after 14 minutes of genuine work and
	// lost all of it, because reserve, design, write, test, gate, commit, push
	// and PR do not fit in 60 turns. Set it high enough that only a loop trips
	// it, and let PerBuilder be what actually bounds a run.
	MaxTurns int

	Concurrency int
	LogDir      string
	Runner      Runner // how to invoke an agent (ADR 0010)
}

func DefaultRunOpts() RunOpts {
	return RunOpts{PerBuilder: 35 * time.Minute, MaxTurns: 300, Concurrency: 3, LogDir: ".flywheel/runs"}
}

// Run executes a plan. It re-checks the kill switch before every builder, not
// just at the start: a night is long, and the point of a kill switch is that it
// works while things are already running.
func Run(r Roster, plan Plan, opts RunOpts) ([]Builder, error) {
	if plan.Stopped != "" {
		return nil, fmt.Errorf("halted: %s", plan.Stopped)
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if err := os.MkdirAll(opts.LogDir, 0o755); err != nil {
		return nil, err
	}

	out := make([]Builder, len(plan.Assignments))
	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup

	for i, a := range plan.Assignments {
		wg.Add(1)
		go func(i int, a Assignment) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			b := Builder{Bead: a.Bead, Repo: a.Repo, Agent: a.Agent, Started: time.Now()}
			repo, ok := r.Repo(a.Repo)
			if !ok {
				b.Outcome, b.Detail = "error", "repo not in roster"
				out[i] = b
				return
			}
			// Re-check: the switch may have been thrown since allocation.
			if why, halted := stopped([]string{repo.Path}); halted {
				b.Outcome, b.Detail = "skipped", "kill switch: "+why
				out[i] = b
				return
			}
			if !opts.Execute {
				b.Outcome, b.Detail = "skipped", "dry run — pass -execute to spawn builders"
				out[i] = b
				return
			}
			out[i] = build(repo, a, opts)
		}(i, a)
	}
	wg.Wait()
	return out, nil
}

// build runs one builder to completion in its own worktree.
func build(repo Repo, a Assignment, opts RunOpts) Builder {
	b := Builder{Bead: a.Bead, Repo: a.Repo, Agent: a.Agent, Started: time.Now()}
	wt := filepath.Join(filepath.Dir(repo.Path), fmt.Sprintf("%s-%s", repo.Name, a.Bead))
	b.Worktree = wt

	// Record the exact commit the worktree starts from. Counting against main
	// would credit the builder with whatever its base branch already carried —
	// the first attempt at this check would have called a no-op green because
	// the worktree was cut from a feature branch, not from main.
	base := headOf(repo.Path)

	// Worktree first: if this fails, nothing has been claimed or edited.
	if err := git2(repo.Path, "worktree", "add", "-b", "bead/"+a.Bead, wt); err != nil {
		b.Outcome, b.Detail = "error", "worktree: "+err.Error()
		b.Took = time.Since(b.Started)
		return b
	}
	defer func() {
		// Always detach the worktree. The branch survives — that is the work.
		_ = git2(repo.Path, "worktree", "remove", "--force", wt)
	}()

	// blackbird ties a name to its FIRST registration token, permanently. A
	// run that registers <repo>/builder and drops the token burns that name —
	// every later run gets UNAUTHENTICATED and invents <repo>/builder-<bead>
	// instead, fragmenting the audit trail one identity at a time (fw-t7d).
	// So the token lives in XDG state, outside every repo, keyed by the stable
	// name, and the skill is told to reuse it.
	if home, err := os.UserHomeDir(); err == nil {
		state := os.Getenv("XDG_STATE_HOME")
		if state == "" {
			state = filepath.Join(home, ".local", "state")
		}
		_ = os.MkdirAll(filepath.Join(state, "flywheel"), 0o700)
	}

	// Write the builder's identity where guard.sh can read it without an
	// env-prefixed invocation the allowlist cannot match.
	if err := os.MkdirAll(filepath.Join(wt, ".flywheel"), 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(wt, ".flywheel", "agent"), []byte(a.Agent+"\n"), 0o644)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.PerBuilder)
	defer cancel()

	logPath := filepath.Join(opts.LogDir, fmt.Sprintf("%s-%s.log", repo.Name, a.Bead))
	logFile, err := os.Create(logPath)
	if err != nil {
		b.Outcome, b.Detail = "error", "log: "+err.Error()
		b.Took = time.Since(b.Started)
		return b
	}
	defer logFile.Close()

	// The builder is told exactly one thing: work this bead through the skill.
	prompt := fmt.Sprintf("/flywheel-next %s", a.Bead)
	runner := opts.Runner.resolved()
	args := runner.argv(prompt)
	if opts.MaxTurns > 0 && runner.Cmd == "claude" {
		// A turn cap is not a universal concept; only pass it to a CLI known
		// to have one. The wall-clock cap bounds every runner regardless.
		args = append(args, "--max-turns", fmt.Sprint(opts.MaxTurns))
	}
	cmd := exec.CommandContext(ctx, runner.Cmd, args...)
	if runner.quietStdin() {
		// The bug that kept the review panel dead for its whole life: an agent
		// CLI reads inherited stdin instead of its prompt.
		if devnull, err := os.Open(os.DevNull); err == nil {
			defer devnull.Close()
			cmd.Stdin = devnull
		}
	}
	cmd.Dir = wt
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Scrub any inherited FLYWHEEL_AGENT before setting this builder's own.
	// The last run reported its audit entries under a name inherited from the
	// parent shell rather than its assigned one — attribution in the ADR 0003
	// log has to come from the assignment, not from whatever the environment
	// happened to carry.
	// hermeticEnv, not os.Environ: a builder that inherited GIT_DIR would edit
	// the spawner's repository from inside its own worktree.
	env := hermeticEnv()
	clean := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "FLYWHEEL_AGENT=") {
			clean = append(clean, kv)
		}
	}
	// Hand the builder the canonical project_key rather than letting it derive
	// one. A builder resolving its own path can disagree with the coordinator
	// under a symlink, and two agents on different keys never conflict.
	cmd.Env = append(clean, "FLYWHEEL_AGENT="+a.Agent, "FLYWHEEL_PROJECT_KEY="+repo.Path)

	// Poll the kill switch while the builder runs. Without this the switch only
	// gated ALLOCATION: a human pulling it at 2am would watch every in-flight
	// builder run to completion, which is not what a kill switch is for.
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if _, halted := stopped([]string{repo.Path}); halted {
					cancel() // kills the child via the context
					return
				}
			}
		}
	}()

	runErr := cmd.Run()
	close(done)
	b.Took = time.Since(b.Started)

	// Exit 0 is NOT success. The first live run blocked on permissions, did
	// nothing, explained itself clearly, and exited 0 — and was reported as
	// green. A builder that produced no commits produced no work, whatever its
	// exit code says, and calling that green is the same class of lie as an
	// empty review ledger reading as "no findings".
	commits := commitsSince(repo.Path, base, "bead/"+a.Bead)

	switch {
	case ctx.Err() == context.DeadlineExceeded:
		b.Outcome = "timeout"
		b.Detail = fmt.Sprintf("exceeded %s — bead left open, see %s", opts.PerBuilder, logPath)
	case runErr != nil:
		b.Outcome = "abandoned"
		b.Detail = fmt.Sprintf("%v — see %s", runErr, logPath)
	case commits == 0:
		b.Outcome = "no-op"
		b.Detail = fmt.Sprintf("agent exited cleanly but committed nothing — read %s before trusting this", logPath)
	default:
		b.Outcome = "green"
		b.Detail = fmt.Sprintf("%d commit(s), see %s", commits, logPath)
	}
	// NOTE: the merge slot is deliberately NOT taken here. It used to be, and it
	// did nothing — this point is after cmd.Run(), and the agent pushes inside
	// its own run, so the slot was acquired and released around an
	// already-pushed branch. Traced: push at …123.813, acquire at …124.635. It
	// cost up to ten minutes of uncancellable wall clock per green builder and
	// prevented no race at all.
	//
	// The push happens in the agent, so the agent holds the slot:
	// /flywheel-next step 7 acquires before pushing and releases after.

	b.Commits = commits

	// fw-lb8.7: a builder killed by the turn cap or wall-clock never runs the
	// skill's abandon path, so the bead stays in_progress with no lease — and
	// reclaim deliberately refuses unleased in-progress beads, because that
	// rule protects a human's work. Nothing then recovers it and the next run
	// reports "not allocatable". The spawner knows the outcome, so it restores.
	if commits == 0 && b.Outcome != "green" {
		bd := bdClient{dir: repo.Path, run: execBD}
		if err := bd.reopen(a.Bead); err == nil {
			_ = bd.note(a.Bead, fmt.Sprintf(
				"Builder %s ended as %s having committed nothing; the spawner returned this bead to open so it is allocatable again (fw-lb8.7). See %s.",
				a.Agent, b.Outcome, logPath))
		}
	}
	return b
}

// headOf returns the commit a worktree will be cut from.
func headOf(dir string) string {
	out, err := inDir(dir, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// commitsSince counts what the builder itself added — commits between the base
// the worktree was cut from and the branch head. This is the only evidence a
// builder did work, and it must not credit whatever the base already carried.
func commitsSince(dir, base, branch string) int {
	if base == "" {
		return 0
	}
	out, err := inDir(dir, "git", "rev-list", "--count", base+".."+branch).Output()
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n
}

func git2(dir string, args ...string) error {
	if out, err := inDir(dir, "git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// Digest is the one artifact a human reads in the morning (fw-lb8.5).
func Digest(bs []Builder) string {
	var sb strings.Builder
	counts := map[string]int{}
	for _, b := range bs {
		counts[b.Outcome]++
	}
	fmt.Fprintf(&sb, "%d builder(s): %d green, %d no-op, %d abandoned, %d timeout, %d error, %d skipped\n",
		len(bs), counts["green"], counts["no-op"], counts["abandoned"], counts["timeout"], counts["error"], counts["skipped"])
	for _, b := range bs {
		fmt.Fprintf(&sb, "  %-22s %-12s %-10s %-8s %s\n",
			b.Repo, b.Bead, b.Outcome, b.Took.Round(time.Second), b.Detail)
	}
	return strings.TrimRight(sb.String(), "\n")
}
