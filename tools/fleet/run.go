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
	"io"
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
	USD      float64       `json:"usd"`
	Tokens   int           `json:"tokens"`
	Turns    int           `json:"turns"`
	// Measured is false when the cost never reached the ledger — either the
	// runner reported none, or recording it failed. Zero spend and UNMEASURED
	// spend mean opposite things (cost.go), and Builder carried no way to tell
	// them apart.
	Measured bool `json:"measured"`
	// RateLimited marks a run that ended because the account ran out of quota
	// rather than because the work failed. It is not a property of the bead,
	// so the bead must not be penalised for it and the fleet must stop
	// dispatching — every remaining builder would buy the same wall.
	RateLimited bool   `json:"rate_limited"`
	Detail      string `json:"detail"`
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
	// BuildersPerRepo is how many builders may work one repo at once. One,
	// always, today: it is what keeps every builder solo in its own repo and
	// therefore free of the reservation requirement. Carried explicitly so the
	// run context can state the reasoning rather than imply it.
	BuildersPerRepo int
	// FleetWidth is set by Run from the workload; reported, never configured.
	FleetWidth int
	LogDir     string
	Runner     Runner // how to invoke an agent (ADR 0010)
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

	// Concurrency follows the workload, bounded two ways.
	//
	// Globally by the cap, because every builder draws on ONE account quota —
	// two of them emptied a session window in 29 minutes. And per repo by
	// exactly one, which is the bound that matters: territory reservations
	// exist to stop two agents editing the same file, and two builders in
	// DIFFERENT repos cannot collide because blackbird keys reservations by
	// project_key. Serialising per repo makes every builder solo in its own
	// repo, so the fleet can widen across repos without re-arming the
	// reservation requirement that cost three runs (fw-k8f).
	//
	// Spawning more builders than there is work is pure startup cost, so the
	// effective width is the number of distinct repos with work.
	repos := map[string]bool{}
	for _, a := range plan.Assignments {
		repos[a.Repo] = true
	}
	width := fleetWidth(plan.Assignments, opts.Concurrency)
	opts.FleetWidth = width
	if opts.BuildersPerRepo < 1 {
		opts.BuildersPerRepo = 1
	}

	out := make([]Builder, len(plan.Assignments))
	sem := make(chan struct{}, width)
	// One slot per repo. Held for the whole build, so a second bead in the
	// same repo waits rather than racing the first one's worktree.
	repoSlot := map[string]chan struct{}{}
	for name := range repos {
		repoSlot[name] = make(chan struct{}, 1)
	}
	var wg sync.WaitGroup

	// A 429 is the account's quota, not this bead's problem: every builder
	// still queued would spend its startup cost to discover the same wall.
	// The first night lost both builders that way, 29 minutes apart, and the
	// second one's $16 bought a second copy of the same error message.
	var quota struct {
		sync.Mutex
		hit  bool
		when string
	}

	// The account rations a period, not a budget, and publishes neither a
	// remaining balance nor a way to query one — so there is nothing to pace
	// against in advance. What it does publish is the moment the quota comes
	// back, in the 429 itself. Follow that: run until it says stop, wait
	// exactly as long as it asks, resume without anyone clicking anything.
	//
	// The earlier gates guessed instead. Dollars per repo per month rationed
	// something a subscription never charges; dollars per window had the right
	// shape and still needed a number that maps to nothing the account states.
	var held bool
	var heldTo time.Time
	var heldWhy string
	if opts.Execute && len(plan.Assignments) > 0 {
		if repo, ok := r.Repo(plan.Assignments[0].Repo); ok {
			heldTo, heldWhy, held = heldUntil(repo.Path, time.Now())
		}
	}

	for i, a := range plan.Assignments {
		wg.Add(1)
		go func(i int, a Assignment) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if slot, ok := repoSlot[a.Repo]; ok {
				slot <- struct{}{}
				defer func() { <-slot }()
			}

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
			if held {
				b.Outcome = "skipped"
				b.Detail = fmt.Sprintf("account quota returns %s (in %s) — not dispatched; %s",
					heldTo.Local().Format("15:04 MST"), time.Until(heldTo).Round(time.Minute), heldWhy)
				out[i] = b
				return
			}
			quota.Lock()
			hit, when := quota.hit, quota.when
			quota.Unlock()
			if hit {
				b.Outcome = "skipped"
				b.Detail = "account out of quota — not dispatched"
				if when != "" {
					b.Detail += " (" + when + ")"
				}
				out[i] = b
				return
			}
			res := build(repo, a, opts)
			if res.RateLimited {
				quota.Lock()
				if !quota.hit {
					quota.hit, quota.when = true, res.Detail
				}
				quota.Unlock()
				// Persist it. The process that learned about the wall is about
				// to exit; the next scheduled run is a different one, and would
				// otherwise pay a full builder startup to rediscover it.
				if reset, err := noteRateLimit(repo.Path, res.Detail, time.Now()); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not record the quota hold: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "quota exhausted; holding until %s\n",
						reset.Local().Format(time.RFC1123))
				}
			}
			out[i] = res
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
	branch := "bead/" + a.Bead
	if err := clearEmptyBranch(repo.Path, base, branch); err != nil {
		b.Outcome, b.Detail = "error", err.Error()
		b.Took = time.Since(b.Started)
		return b
	}
	if err := git2(repo.Path, "worktree", "add", "-b", branch, wt); err != nil {
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
	if runner.Cmd == "claude" {
		args = append(args, "--disallowed-tools", unattendedDenials)
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
	// Tee: the log file stays the human artifact, and the buffer lets us read
	// the runner's cost report out of the same stream.
	var captured strings.Builder
	cmd.Stdout = io.MultiWriter(logFile, &captured)
	cmd.Stderr = logFile
	// Scrub any inherited FLYWHEEL_AGENT before setting this builder's own.
	// The last run reported its audit entries under a name inherited from the
	// parent shell rather than its assigned one — attribution in the ADR 0003
	// log has to come from the assignment, not from whatever the environment
	// happened to carry.
	// hermeticEnv, not os.Environ: a builder that inherited GIT_DIR would edit
	// the spawner's repository from inside its own worktree.

	// Hand the builder the canonical project_key rather than letting it derive
	// one. A builder resolving its own path can disagree with the coordinator
	// under a symlink, and two agents on different keys never conflict.
	// The builder cannot read its own environment — the sandbox refuses
	// /proc/self/environ outright ("may expose secrets") and env/printenv are
	// not allowlisted — so anything it must ACT on has to arrive as a file in
	// its worktree or as its prompt. Identity still goes in the environment
	// for anything that shells out, but the run's shape goes in a file the
	// builder can actually read (fw-k8f).
	if err := writeRunContext(wt, a, opts, opts.BuildersPerRepo, opts.FleetWidth); err != nil {
		b.Outcome, b.Detail = "error", "run context: "+err.Error()
		b.Took = time.Since(b.Started)
		return b
	}

	// withIdentity, because the builder cannot read its own token: the sandbox
	// scopes reads to the worktree and ADR 0003 forbids agents reading secrets
	// anyway. The coordinator is under neither constraint, so it hands the
	// identity over rather than sending the builder to look for it (fw-eoi).
	cmd.Env = withIdentity(
		append(withAgent(hermeticEnv(), a.Agent), "FLYWHEEL_PROJECT_KEY="+repo.Path),
		repo.Name)

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

	b.Outcome = classify(runErr, commits, ctx.Err() == context.DeadlineExceeded)
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		b.Detail = fmt.Sprintf("exceeded %s — bead left open, see %s", opts.PerBuilder, logPath)
	case runErr != nil && commits > 0:
		// Exit 0 is not success, and the converse was never handled: a builder
		// that made five commits and opened a PR before dying on a 429 at the
		// very end was reported as "abandoned" with no mention of either, and
		// the night read as a total loss. Non-zero exit is not proof of no
		// work — the commits are the evidence, in both directions.
		b.Detail = fmt.Sprintf("%d commit(s) survived, but the run ended on %v — read %s before merging",
			commits, runErr, logPath)
	case runErr != nil:
		b.Detail = fmt.Sprintf("%v — see %s", runErr, logPath)
	case commits == 0:
		b.Detail = fmt.Sprintf("agent exited cleanly but committed nothing — read %s before trusting this", logPath)
	default:
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

	// Record what it cost, from the runner's own report — never an estimate.
	// A runner that reports nothing leaves the run unmeasured, which cost.go
	// renders as "unmeasured" rather than as free.
	if rep, ok := parseRunReport([]byte(captured.String())); ok {
		b.USD, b.Tokens, b.Turns = rep.TotalCostUSD, rep.tokens(), rep.NumTurns
		// Orthogonal to the outcome: the run still failed, but Run() needs to
		// know the wall was the account's quota so it stops dispatching into
		// it. The bead itself is not at fault and must not look like it is.
		if rep.rateLimited() {
			b.RateLimited = true
			if rep.Result != "" {
				b.Detail = rep.Result + " — " + b.Detail
			}
		}
		// Never swallowed. A cost that did not reach the ledger is not a
		// measurement — `fleet cost` reads the ledger, so reporting Measured
		// here while the ledger stayed empty is the split cost.go was written
		// to prevent. That is how this recording no-opped silently once.
		if err := logSpend(repo.Path, a, rep); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s\n", err)
		} else {
			b.Measured = true
		}
	}

	b.Commits = commits

	// fw-lb8.7: a builder killed by the turn cap or wall-clock never runs the
	// skill's abandon path, so the bead stays in_progress with no lease — and
	// reclaim deliberately refuses unleased in-progress beads, because that
	// rule protects a human's work. Nothing then recovers it and the next run
	// reports "not allocatable". The spawner knows the outcome, so it restores.
	if commits == 0 && b.Outcome != "green" {
		bd := bdClient{dir: repo.Path, run: execBD}
		// Never reopen a bead somebody legitimately closed. A builder can
		// correctly finish with no commits — "already done elsewhere, nothing
		// to change" — and blindly reopening discards that close reason and
		// makes the fleet redo the work.
		if cur, err := bd.show(a.Bead); err == nil && cur.Status == "closed" {
			_ = bd.note(a.Bead, fmt.Sprintf(
				"Builder %s ended as %s with no commits, but this bead is closed — leaving it closed. See %s.",
				a.Agent, b.Outcome, logPath))
		} else if err := bd.reopen(a.Bead); err == nil {
			// Clear the lease too. Reopening while leaving lease metadata
			// behind produced a bead that was open, annotated as allocatable,
			// filtered out by allocate, and reported as "blocked by an open
			// dependency or gate" — which it was not.
			if cur.Metadata != nil {
				md := copyMD(cur.Metadata)
				delete(md, leaseHolderKey)
				delete(md, leaseExpiresKey)
				_ = bd.setMetadata(a.Bead, md)
			}
			_ = bd.note(a.Bead, fmt.Sprintf(
				"Builder %s ended as %s having committed nothing; the spawner returned this bead to open so it is allocatable again (fw-lb8.7). See %s.",
				a.Agent, b.Outcome, logPath))
		}
	}
	return b
}

// headOf returns the commit a worktree will be cut from.
func headOf(dir string) string { return gitLine(dir, "rev-parse", "HEAD") }

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

// classify names a builder's outcome from evidence, not from its exit code.
//
// Both directions matter and each was learned the hard way. Exit 0 is not
// success: the first live run blocked on permissions, explained itself, and
// exited 0 with nothing committed. Exit non-zero is not failure either: the
// first unattended night ended on a 429 after five commits and a pull request,
// and reporting that as "abandoned" wrote off work that was already pushed.
func classify(runErr error, commits int, timedOut bool) string {
	switch {
	case timedOut:
		return "timeout"
	case runErr != nil && commits > 0:
		return "salvage"
	case runErr != nil:
		return "abandoned"
	case commits == 0:
		return "no-op"
	default:
		return "green"
	}
}

// clearEmptyBranch removes a leftover bead branch that carries no work, so a
// bead can be attempted twice.
//
// A builder that commits nothing still leaves bead/<id> behind — the worktree
// is detached on the way out but the branch survives, because the branch IS the
// work whenever there is any. When there is none, that leftover makes the next
// `worktree add -b` fail with "a branch named 'bead/<id>' already exists", and
// the bead can never be attempted again: one no-op locks it forever. The queue
// self-locks one bead at a time, and nothing says why.
//
// A branch carrying content is never touched. That is reconcile's rule and it
// is the important half — losing an agent's pushed work to a tidy-up is far
// worse than a stuck bead, so this refuses rather than guesses.
//
// "Carrying content" is judged by tree, not by commit count. Counting
// base..branch is right until main's SHAs change underneath it: after a history
// rewrite every stale branch reads as the whole pre-rewrite history — bead/fw-ax2
// showed 23 "unmerged commits" and zero work — and is permanently un-sweepable,
// which is the lock #50 removed, back by another door (fw-web). A rewrite
// preserves every tree, so a branch that added nothing has the same tree as some
// commit on main between the merge-base and today's head: the merge-base itself
// in the ordinary case, the rewritten twin of its old tip after a rewrite. The
// window starts at the merge-base on purpose — matching an *older* main tree
// would mean the branch deleted something main still has, and that is work.
func clearEmptyBranch(repoPath, base, branch string) error {
	if err := git2(repoPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return nil // no such branch; nothing to clear
	}
	// Any worktree still holding it is a live builder, not a leftover. Not
	// being able to list worktrees is not evidence that there are none.
	out, err := inDir(repoPath, "git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("worktree: cannot list worktrees, so cannot tell whether %s is live; not touching it", branch)
	}
	if strings.Contains(string(out), "branch refs/heads/"+branch+"\n") {
		return fmt.Errorf("worktree: %s is checked out in another worktree — "+
			"a builder may still be running; not touching it", branch)
	}
	if base == "" {
		return fmt.Errorf("worktree: %s already exists and no base commit was recorded to judge it against; not touching it", branch)
	}
	mb := gitLine(repoPath, "merge-base", base, branch)
	if mb == "" {
		return fmt.Errorf("worktree: %s already exists and shares no history with %s — "+
			"refusing to guess whether it is work; merge or drop it by hand", branch, base)
	}
	tree := gitLine(repoPath, "rev-parse", branch+"^{tree}")
	if tree != "" && mainHasTree(repoPath, mb, base, tree) {
		if err := git2(repoPath, "branch", "-D", branch); err != nil {
			return fmt.Errorf("worktree: could not clear the empty leftover %s: %w", branch, err)
		}
		return nil
	}
	// Count against base, not the merge-base: after a rewrite the merge-base
	// sits behind everything that was rewritten, and the count would include
	// files main already has. A count of zero means git could not tell us.
	if n := filesBetween(repoPath, base, branch); n > 0 {
		return fmt.Errorf("worktree: %s already exists with %d file(s) changed against %s — "+
			"refusing to delete an agent's work; merge or drop it by hand", branch, n, base)
	}
	return fmt.Errorf("worktree: %s already exists with content %s does not have — "+
		"refusing to delete an agent's work; merge or drop it by hand", branch, base)
}

// mainHasTree reports whether tree is the tree of mb or of any commit on
// mb..base — i.e. whether main already holds exactly this content.
func mainHasTree(dir, mb, base, tree string) bool {
	// mb gets its own check: `--not mb` excludes it, and `mb^..base` would
	// fail whenever the merge-base is a root commit.
	if gitLine(dir, "rev-parse", mb+"^{tree}") == tree {
		return true
	}
	out, err := inDir(dir, "git", "log", "--format=%T", base, "--not", mb).Output()
	if err != nil {
		return false
	}
	for _, t := range strings.Fields(string(out)) {
		if t == tree {
			return true
		}
	}
	return false
}

// filesBetween counts the paths that differ between two revisions; the number
// in a refusal, so a human sees the size of the work rather than a SHA count
// that a rewrite can inflate.
func filesBetween(dir, from, to string) int {
	out, err := inDir(dir, "git", "diff", "--name-only", from, to).Output()
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(out)))
}

// gitLine runs a git command expected to print one line and returns it
// trimmed, or "" on any failure.
func gitLine(dir string, args ...string) string {
	out, err := inDir(dir, "git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// fleetWidth is how many builders to run at once: as many as there are repos
// with work, never more than the ceiling, never fewer than one.
//
// The ceiling bounds the account's quota — every builder draws on one session
// window, and two of them emptied it in 29 minutes. The workload does the
// sizing, because a builder spawned where there is nothing to do still pays
// full startup, which is what the last three runs spent to reach a wall.
func fleetWidth(as []Assignment, ceiling int) int {
	repos := map[string]bool{}
	for _, a := range as {
		repos[a.Repo] = true
	}
	w := len(repos)
	if ceiling > 0 && w > ceiling {
		w = ceiling
	}
	if w < 1 {
		w = 1
	}
	return w
}

// unattendedDenials is the part of ADR 0003 that must NOT live in
// .claude/settings.json.
//
// A deny in that file applies to whoever reads it, and the maintainer reads it
// too — so keeping `gh pr merge` there meant nobody could merge, and deleting
// it there would hand merge authority to every builder. A deny also beats an
// allow from any file, so a session-scoped grant cannot override it; that was
// measured, not assumed.
//
// Attaching it to the spawn separates the two: the supervised session merges,
// the unattended builder cannot. It is also the stronger place for it. A
// builder can edit .claude/settings.json inside its own worktree; it cannot
// edit the flags it was launched with. ADR 0003's amendment already says the
// allowlist is defence in depth rather than a sandbox — this moves the one
// rule that has to differ between the two callers out of the shared file.
//
// The rest of ADR 0003's denials stay in settings.json, where they apply to
// both, because neither party should be tagging, releasing or reading secrets.
const unattendedDenials = "Bash(gh pr merge:*)"
