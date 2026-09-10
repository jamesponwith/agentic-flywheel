// gh.go — the one place the fleet asks gh anything about a repo (fw-64x).
//
// ghPRs, ghDefaultBranch and ghReviewLoad each used to hardcode
// "jamesponwith/"+repo.Name and reimplement the exec -> ExitError stderr wrap
// -> unmarshal idiom separately, so changing the owner — or adding
// --hostname for GH Enterprise later — was a three-site edit. Now it is one.
package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ghRepo is the owner/name slug gh's --repo wants. repo.Owner comes from the
// roster's GitHubOwner (LoadRoster); DefaultGitHubOwner covers a Repo built
// outside LoadRoster (doctor -self, tests) rather than shelling out with a
// bare "/name" that gh would reject.
func ghRepo(repo Repo) string {
	owner := repo.Owner
	if owner == "" {
		owner = DefaultGitHubOwner
	}
	return owner + "/" + repo.Name
}

// ghJSON runs `gh <args...> --repo <owner>/<name> --json <fields>` and
// unmarshals stdout into v. Keeps stderr on failure: "auth expired", "rate
// limited" and "no such repo" are different problems, and a caller's refusal
// to plan should say which.
func ghJSON(repo Repo, args []string, fields string, v any) error {
	full := append(append([]string{}, args...), "--repo", ghRepo(repo), "--json", fields)
	out, err := exec.Command("gh", full...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return json.Unmarshal(out, v)
}

// resolveDefaultBranch fills repo.DefaultBranch by asking gh, unless already
// known. Callers pass a *Repo pointing into a Roster's Repos slice so the
// answer is persisted once per roster load — Repos is shared backing array
// even when the Roster itself is passed by value — rather than re-asked by
// every command that needs it (fw-64x, fw-boy).
func resolveDefaultBranch(repo *Repo) error {
	if repo.DefaultBranch != "" {
		return nil
	}
	branch, err := ghDefaultBranch(*repo)
	if err != nil {
		return err
	}
	repo.DefaultBranch = branch
	return nil
}
