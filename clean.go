package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// removeOne deletes a worktree, re-checking at the moment of deletion that it
// still holds nothing: the scan may be seconds old. It returns "removed",
// "skipped" (guard refused; nothing touched) or "failed" (git refused).
//
// Registered worktrees go through `git worktree remove` only — there is no
// rm -rf fallback, so git's own refusals (locked, dirty, submodules) stand.
// Orphans have no registration to remove; they are deleted directly, but
// only after the same guard if they turn out to contain a repository.
func removeOne(w *Worktree, deleteBranch bool) (outcome, detail string) {
	if hasGit(w.Path) {
		fresh := &Worktree{Path: w.Path}
		factsFor(fresh, registry{dates: map[string]int64{}}, false)
		switch {
		case fresh.Unreadable:
			return "skipped", "git cannot read it"
		case fresh.Dirty > 0:
			return "skipped", fmt.Sprintf("became dirty since the scan (%d files)", fresh.Dirty)
		case fresh.Unpushed > 0:
			return "skipped", fmt.Sprintf("has %d commits on no remote", fresh.Unpushed)
		case fresh.Ignored != "" && w.Ignored == "":
			return "skipped", "ignored file appeared: " + fresh.Ignored
		}
	}
	if w.Verdict == Orphan {
		// An orphan has no registration, so there is nothing to prune. A
		// repo-wide `git worktree prune` would also drop registrations the
		// user never selected, such as a worktree on an unplugged drive.
		if err := os.RemoveAll(w.Path); err != nil {
			return "failed", err.Error()
		}
		return "removed", ""
	}
	if _, err := git(w.Repo, "worktree", "remove", w.Path); err != nil {
		return "failed", strings.TrimSpace(err.Error())
	}
	if deleteBranch && w.Branch != "" && w.Branch != "(detached)" && w.Branch != "(none)" {
		_, _ = git(w.Repo, "branch", "-d", w.Branch)
	}
	return "removed", ""
}

func hasGit(p string) bool {
	_, err := os.Stat(filepath.Join(p, ".git"))
	return err == nil
}
