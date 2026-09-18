# warren

`mole` keeps your Mac tidy. **warren** keeps your Claude Code worktrees tidy.

Every time Claude Code isolates a task it creates `<repo>/.claude/worktrees/<name>`
— a full checkout. Nothing ever deletes them. On the machine this was written on
they had reached 114 worktrees across 19 repos and about 12 GB.

`warren` finds them all, works out which ones still hold something you would miss,
and removes only the rest.

```
warren list       every worktree, with a keep-or-drop verdict
warren analyze    where the disk went
warren clean      remove the ones whose PR is merged or closed
warren prune      fix stale registrations and orphaned directories
warren            interactive menu
```

A single static Go binary, no dependencies beyond `git` (and `gh` if you want PR
state). A warm machine-wide scan of 114 worktrees takes about 3 seconds.

## The verdicts

| verdict | meaning | removed by |
|---|---|---|
| `reclaim` | PR merged or closed, tree clean, every commit on a remote | `clean` |
| `idle`    | clean and pushed, but no PR — your call | `clean --idle` only |
| `orphan`  | a directory git no longer tracks | `clean` |
| `hold`    | see below | never |

A worktree is **held** — and no flag removes it — when any of these is true:

- it has uncommitted changes, including untracked files;
- it has commits that are on no remote (`git log HEAD --not --remotes`);
- it contains a gitignored file that looks like secrets or local config: `.env*`,
  `*.local`, `*.pem`/`*.key`, credentials, local databases, `*.tfvars`… Build
  output, caches and logs are ignored for a reason and never count.
  `--allow-ignored` turns this off;
- git has it locked (`git worktree lock`);
- some process has it as its working directory — a live Claude session, a shell,
  an editor;
- its PR is open;
- git cannot read it at all.

## Why it is safe

Deleting a worktree deletes a real checkout, so `warren` is deliberately paranoid.

- **The data-loss guard is git, not GitHub.** Clean tree and no commit outside the
  remotes — that holds with no network and no `gh`.
- **Squash merges are handled.** Most repos squash on merge, so a merged branch is
  never an ancestor of `main` and `git branch --merged` reports nothing. `warren`
  asks GitHub for the real PR state (`gh`, cached six hours). If GitHub is
  unreachable it keeps the last good answer rather than forgetting it.
- **It re-checks at the moment of deletion.** The scan can be seconds stale, so
  every removal re-runs the dirty, unpushed and ignored-file checks and refuses if
  anything changed. `--yes` does not bypass this.
- **Registered worktrees are only ever removed with `git worktree remove`.** There
  is no `rm -rf` fallback, so git's own refusals — locked, dirty, submodules —
  stand. Only unregistered orphan directories are deleted directly, and even those
  go through the guard if they turn out to contain a repository.
- **One unreadable worktree does not hide the others.** It is reported as held.

Every removal is logged to `~/.config/warren/history.log` (`warren history`).

## Using it from Claude

`install.sh` links a Claude Code skill into `~/.claude/skills/warren`, so you can say:

> use warren to clean up worktrees

Claude will scope the scan (`--here` for the repo you're in), plan with
`warren clean --dry-run --json`, tell you what it would remove **and what it is
holding back and why**, and only then ask before executing. It will not pass
`--yes`, `--idle`, `--branches` or `--allow-ignored` on its own.

Every command takes `--json`. `clean --json` refuses to remove anything without
`--yes` (exit 2), so an agent has to plan before it can delete:

```sh
warren clean --dry-run --json --here
```
```json
{
  "dry_run": true, "scope": "/path/to/repo",
  "planned": 3, "reclaimable_kb": 35556, "held": 4, "idle": 2,
  "worktrees":      [{ "worktree": "fix-login-redirect", "verdict": "RECLAIM", "reason": "PR #412 merged", "outcome": "planned" }],
  "held_worktrees": [{ "worktree": "add-retry-logic",    "reason": "1 uncommitted" }],
  "idle_worktrees": [{ "worktree": "spike-caching",      "reason": "no PR, fully pushed" }]
}
```

`outcome` is `planned`, `removed`, `skipped` (the guard refused at deletion time)
or `failed` (git refused). Exit status: 0, or 1 if anything failed, or 2 if the
call was refused.

## Install

```sh
go install github.com/hikvineh/warren@latest     # binary only
```
or, for the binary, the `wr` alias and the Claude skill:
```sh
git clone https://github.com/hikvineh/warren && cd warren && ./install.sh
```

Requires `git`; `lsof` for live-session detection (stock on macOS and most
Linux); optionally [`gh`](https://cli.github.com) for PR state. Without `gh`,
everything unmerged reads as `idle`. Tested in CI on macOS and Linux.

## Usage

```sh
warren list                  # the full picture
warren list --hold           # only what is being protected, and why
warren list --only idle      # one verdict
warren list --size           # measure disk too (slower)
warren list --json

warren analyze               # biggest repos, biggest worktrees, reclaimable total

warren clean --dry-run       # preview; changes nothing
warren clean                 # remove merged/closed worktrees, after confirming
warren clean --idle          # also drop pushed worktrees that have no PR
warren clean --branches      # delete the local branch too
warren clean --allow-ignored # don't hold worktrees just for a gitignored .env

warren prune --dry-run       # stale git registrations + orphan directories
cd "$(warren path my-feature)"
```

`--here` (the repo you're in, even from inside a worktree) and `--repo <path>` work
on `list`, `analyze`, `clean`, `prune` and `path`.

By default `warren` scans `~/Developer` (then `~/Projects`, `~/code`, `~/src`,
then `$HOME`). Change that with `warren paths add <dir>` / `remove` / `reset`.

| variable | effect |
|---|---|
| `WARREN_NO_GH=true` | skip GitHub entirely; rely on git alone |
| `WARREN_JOBS=N` | parallel git calls (default: number of CPUs) |
| `WARREN_PR_TTL=S` | PR cache lifetime in seconds (default 21600) |
| `WARREN_NO_DEFAULT_ROOTS=1` | never fall back to the default roots (used by the tests) |

## Tests

```sh
go test ./...
```

The suite builds a throwaway repo with one worktree per case — clean and pushed,
dirty, committed-but-on-no-remote, orphan, detached, locked, a gitignored `.env`, a
broken `.git` file, a process parked inside one — and asserts the verdicts and,
above all, that removal leaves every one that holds work alone. It also covers the
GitHub path with a stubbed `gh`: merged, open and closed PRs, the per-branch
fallback, and that an offline `gh` keeps the stale cache instead of wiping it.

Tests never scan outside their fixture: every fixture writes an explicit paths file
and the test binary sets `WARREN_NO_DEFAULT_ROOTS`.

## License

MIT — see [LICENSE](LICENSE). Inspired by [mole](https://github.com/tw93/mole), but
shares no code with it.
