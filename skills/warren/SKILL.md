---
name: warren
description: "Find and safely remove leftover Claude Code worktrees under <repo>/.claude/worktrees. Use when the user asks to clean up, list, or audit worktrees, asks what is taking up disk space in their repos, wonders which worktrees are safe to delete, or names warren directly. Removes only worktrees whose PR is merged or closed and that hold no uncommitted or unpushed work."
---

## What this is

Claude Code creates `<repo>/.claude/worktrees/<name>` for every isolated task and
never removes them. They accumulate silently — a hundred-plus worktrees and many GB
across every repo the user has worked in.

`warren` scans for them, decides which ones still hold work the user would miss,
and removes only the rest. Deleting a worktree deletes a real checkout, so treat
every removal as destructive and follow the workflow below.

## Workflow

**1. Decide the scope.** If the user means the repo they are in ("this repo",
"here", or they are clearly working in one), pass `--here`. If they mean
everything ("all my worktrees", "my machine"), pass nothing. Ask only if it is
genuinely unclear — `--here` is the safer guess.

**2. Plan first, always.** Never remove anything before showing the user what
would go:

```sh
warren clean --dry-run --json --here
```

A machine-wide scan takes a few seconds; one repo, about one.

**3. Report the plan in your own words.** From the JSON, tell the user:
- how many worktrees would be removed and how much disk that frees
  (`planned`, `reclaimable_kb`);
- **what is being held back and why** (`held_worktrees`, each with a `reason`) —
  this is the most useful part. "4 held: two have uncommitted changes, one has an
  open PR, one has a `.env`" turns a delete command into a decision they can make;
- how many are idle (`idle`, `idle_worktrees`): clean and pushed but with no PR.
  Mention them; do not remove them unless asked.

**4. Get explicit approval, then execute:**

```sh
warren clean --yes --json --here
```

Report `removed`, `reclaimed_kb`, and any `failed` or `skipped`. A `skipped`
worktree changed between the scan and the deletion and was deliberately left
alone — that is warren working correctly, not an error. `failed` means git itself
refused (for example a locked worktree); say so and leave it.

Skip step 4 entirely if the user only asked what exists.

## Commands

```sh
warren list --json              # every worktree with its verdict
warren list --json --here       # just this repo
warren list --json --only hold  # one verdict: hold, reclaim, idle, orphan
warren clean --dry-run --json   # plan a removal, change nothing
warren clean --yes --json       # execute a removal
warren prune --dry-run --json   # stale registrations and orphan directories
warren analyze                  # human-readable disk breakdown
warren path <name>              # print a worktree's path, for cd
```

`--here` and `--repo <path>` work on `list`, `analyze`, `clean`, `prune` and
`path`. Without `--json` the output is formatted for a person, which is fine when
the user wants to look at it themselves.

## The verdicts

| verdict | meaning | removed by |
|---|---|---|
| `RECLAIM` | PR merged or closed, clean, every commit on a remote | `clean` |
| `IDLE` | clean and pushed, but no PR — ambiguous | `clean --idle` only |
| `ORPHAN` | a directory git no longer tracks | `clean` |
| `HOLD` | uncommitted changes, commits on no remote, a gitignored secret or local config file, locked, a live session, an open PR, or git cannot read it | never |

## What you can rely on

- **`HOLD` is absolute.** No flag removes a held worktree, including `--yes`.
  If the user wants one gone, they deal with the work in it first.
- **The data-loss check is git, not GitHub.** Removable only when `git status`
  is clean and `git log HEAD --not --remotes` is empty. Holds with no network.
- **It re-checks at the moment of deletion** and refuses if anything changed.
  That is where `skipped` comes from.
- **Registered worktrees are only removed through `git worktree remove`** — no
  `rm -rf` fallback — so git's own refusals stand.
- **`warren clean --json` without `--yes` refuses and exits 2** with
  `{"error":"refusing to remove without --yes"}`. Plan with `--dry-run` first;
  do not retry blindly.
- **Exit status:** 0 on success, 1 if any removal failed, 2 if the call was
  refused or the flags were wrong.

## Do not

- Do not pass `--yes` in the same breath as the first scan. Plan, report, then ask.
- Do not pass `--idle` unless the user has said they want worktrees with no PR
  removed. Idle ones are often work in progress that simply has no PR yet.
- Do not pass `--branches` (deletes the local branch too) unless asked.
- Do not pass `--allow-ignored` unless the user has said the flagged file
  (typically a `.env`) is disposable.
- Do not work around a `HOLD` by deleting the directory yourself with `rm -rf`.
  If the user insists, tell them what is in it first.
- Do not run `warren clean` as a "cleanup" step after some other task. Removing
  worktrees is only ever the thing the user asked for.
