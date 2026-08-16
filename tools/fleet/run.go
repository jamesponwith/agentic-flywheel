// run: turn a plan into actual builders.
//
// The coordinator allocates and never edits code (ADR 0006); this is the piece
// that hands each assignment to a builder and gets out of the way. Every
// builder is an isolated `claude -p "/flywheel-next <bead>"` in its own
// worktree, bound by ADR 0003 — branch, commit, gate, PR, and stop.
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
	Outcome  string        `json:"outcome"` // green | abandoned | timeout | error | skipped
	Detail   string        `json:"detail"`
}

// RunOpts bounds a night. Every field here exists because something without it
// would be unbounded.
type RunOpts struct {
	Execute     bool          // false = plan only; the default is not to act
	PerBuilder  time.Duration // wall-clock cap per builder
	MaxTurns    int           // agent turn cap, so a confused builder stops
	Concurrency int
	LogDir      string
}

func DefaultRunOpts() RunOpts {
	return RunOpts{PerBuilder: 25 * time.Minute, MaxTurns: 60, Concurrency: 3, LogDir: ".flywheel/runs"}
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
	args := []string{"-p", prompt}
	if opts.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprint(opts.MaxTurns))
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = wt
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "FLYWHEEL_AGENT="+a.Agent)

	runErr := cmd.Run()
	b.Took = time.Since(b.Started)

	switch {
	case ctx.Err() == context.DeadlineExceeded:
		b.Outcome = "timeout"
		b.Detail = fmt.Sprintf("exceeded %s — bead left open, see %s", opts.PerBuilder, logPath)
	case runErr != nil:
		b.Outcome = "abandoned"
		b.Detail = fmt.Sprintf("%v — see %s", runErr, logPath)
	default:
		b.Outcome = "green"
		b.Detail = "see " + logPath
	}
	return b
}

func git2(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
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
	fmt.Fprintf(&sb, "%d builder(s): %d green, %d abandoned, %d timeout, %d error, %d skipped\n",
		len(bs), counts["green"], counts["abandoned"], counts["timeout"], counts["error"], counts["skipped"])
	for _, b := range bs {
		fmt.Fprintf(&sb, "  %-22s %-12s %-10s %-8s %s\n",
			b.Repo, b.Bead, b.Outcome, b.Took.Round(time.Second), b.Detail)
	}
	return strings.TrimRight(sb.String(), "\n")
}
