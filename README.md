# warren

Find and safely remove leftover Claude Code git worktrees.

[![CI](https://github.com/HikvIneH/warren/actions/workflows/ci.yml/badge.svg)](https://github.com/HikvIneH/warren/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/HikvIneH/warren)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

![warren list: every worktree with a keep-or-drop verdict and the reason](docs/demo.png)

## Why

Claude Code creates `<repo>/.claude/worktrees/<name>`, a full checkout, whenever it
isolates a task. Many get cleaned up, but the rest pile up: on the author's machine
there were 114 of them across 19 repos, about 12 GB.

warren finds them, works out which ones still hold something you would miss, and
removes only the rest. You may not need it: Claude Code now cleans up many of these
itself, and [mole](https://github.com/tw93/mole) will show you how much space the
remainder take. This is a small pet project shared in case it is useful; expect
rough edges.

## Features

- Scans every repo under your project directories and lists each Claude worktree
  with a keep-or-drop verdict and the reason.
- Detects merged and closed PRs, including squash merges, through `gh`.
- Holds anything with uncommitted changes, unpushed commits, secrets or local
  config in ignored files, a lock, a live process, or an open PR.
- Re-checks every worktree at the moment of deletion.
- `--json` output on `list`, `analyze`, `clean` and `prune`, plus a Claude Code
  skill so an agent can plan before it deletes.
- Single static Go binary. Needs only `git` (and optionally `gh`). A warm scan of
  114 worktrees takes about 3 seconds.

## Installation

Requires `git`; `lsof` for live-session detection (stock on macOS and most Linux);
optionally [`gh`](https://cli.github.com) for PR state. Without `gh`, everything
unmerged reads as `idle`. Tested in CI on macOS and Linux.

**With Go**

```sh
go install github.com/hikvineh/warren@latest
```

**Prebuilt binaries** for macOS and Linux (amd64, arm64) are published on the
[releases page](https://github.com/HikvIneH/warren/releases), built with GoReleaser.

**From a clone**: `install.sh` builds the binary, adds a `wr` alias and links the
Claude Code skill in one go.

```sh
git clone https://github.com/hikvineh/warren && cd warren && ./install.sh
```

**Claude Code plugin** (the skill only; install the binary separately):

```
/plugin marketplace add HikvIneH/warren
/plugin install warren@warren
```

## Quick start

```sh
warren list                  # every worktree, with a verdict
warren clean --dry-run       # preview what would be removed; changes nothing
warren clean                 # remove merged/closed worktrees, after confirming
```

Run `warren` with no arguments for an interactive menu.

## Usage

| Command | Description |
|---|---|
| `warren` | Interactive menu (falls back to `list` when stdin is not a terminal) |
| `warren list` | Every worktree with a verdict (aliases: `ls`, `status`) |
| `warren analyze` | Biggest repos, biggest worktrees, reclaimable total (alias: `disk`) |
| `warren clean` | Remove worktrees whose PR is merged or closed |
| `warren prune` | Fix stale git registrations and orphan directories |
| `warren path <name>` | Print a worktree's path, e.g. `cd "$(warren path my-feature)"` (alias: `cd`) |
| `warren paths` | Show or edit the directories to scan: `paths add <dir>`, `remove`, `reset` |
| `warren history` | What warren has removed (alias: `log`) |
| `warren completion` | Shell tab completion: `eval "$(warren completion)"` |
| `warren --help`, `--version` | Help and version |

| Flag | Applies to | Effect |
|---|---|---|
| `--here` | list, analyze, clean, prune, path | Only the repo you are in (works inside a worktree) |
| `--repo <path>` | list, analyze, clean, prune, path | Only the repo owning `<path>` |
| `--json` | list, analyze, clean, prune | Machine-readable output |
| `--dry-run`, `-n` | clean, prune | Preview; never delete |
| `--yes`, `-y` | clean | Skip the confirmation prompt |
| `--idle` | clean | Also drop pushed worktrees that have no PR |
| `--branches` | clean | Delete the local branch too |
| `--allow-ignored` | clean | Do not hold worktrees just for gitignored files |
| `--only <verdict>` | list | Show one verdict; `--hold`, `--reclaim`, `--orphan` are shorthands |
| `--size`, `-s` | list | Measure disk usage too (slower) |

Examples:

```sh
warren list --hold           # only what is being protected, and why
warren list --only idle      # one verdict
warren clean --idle          # also drop pushed worktrees that have no PR
warren prune --dry-run       # stale registrations + orphan directories
```

### Verdicts

| Verdict | Meaning | Removed by |
|---|---|---|
| `reclaim` | PR merged or closed, tree clean, every commit on a remote | `clean` |
| `idle` | Clean and pushed, but no PR; your call | `clean --idle` only |
| `orphan` | A directory git no longer tracks | `clean` |
| `hold` | Holds work or is in use; see Safety guarantees | never |

### JSON and agents

`clean --json` refuses to remove anything without `--yes` (exit 2), so an agent has
to plan before it can delete:

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

### Using it from Claude Code

With the skill installed you can say "use warren to clean up worktrees". Claude
scopes the scan (`--here` for the repo you are in), plans with
`warren clean --dry-run --json`, tells you what it would remove and what it is
holding back and why, and only then asks before executing. It will not pass
`--yes`, `--idle`, `--branches` or `--allow-ignored` on its own.

## Configuration

By default warren scans `~/Developer`, then `~/Projects`, `~/code`, `~/src`, then
`$HOME`. Change that with `warren paths add <dir>`, `warren paths remove <dir>` or
`warren paths reset`. The list is stored in `~/.config/warren/paths`.

| Variable | Effect |
|---|---|
| `WARREN_NO_GH=true` | Skip GitHub entirely; rely on git alone |
| `WARREN_JOBS=N` | Parallel git calls (default: number of CPUs) |
| `WARREN_PR_TTL=S` | PR cache lifetime in seconds (default 21600, six hours) |
| `WARREN_MAXDEPTH=N` | How deep to search for repos under each root (default 7) |
| `WARREN_CONFIG_DIR`, `WARREN_CACHE_DIR`, `WARREN_PATHS_FILE` | Override where config, cache and the paths file live (defaults follow `XDG_CONFIG_HOME` and `XDG_CACHE_HOME`) |
| `WARREN_NO_DEFAULT_ROOTS=1` | Never fall back to the default roots (used by the tests) |

## Safety guarantees

Deleting a worktree deletes a real checkout, so warren is deliberately paranoid.

A worktree is **held**, and no flag removes it, when any of these is true:

- it has uncommitted changes, including untracked files;
- it has commits that are on no remote (`git log HEAD --not --remotes`);
- it contains a gitignored file that looks like secrets or local config: `.env*`,
  `*.local`, `*.pem`/`*.key`, credentials, local databases, `*.tfvars` and similar.
  Build output, caches and logs never count. `--allow-ignored` turns this check off;
- git has it locked (`git worktree lock`);
- some process has it as its working directory (a live Claude session, a shell, an
  editor);
- its PR is open;
- git cannot read it at all.

In addition:

- **The data-loss guard is git, not GitHub.** A clean tree and no commit outside the
  remotes holds with no network and no `gh`.
- **It re-checks at the moment of deletion.** The scan can be seconds stale, so every
  removal re-runs the dirty, unpushed and ignored-file checks and refuses if anything
  changed. `--yes` does not bypass this.
- **Registered worktrees are only removed with `git worktree remove`.** There is no
  `rm -rf` fallback, so git's own refusals (locked, dirty, submodules) stand. Only
  unregistered orphan directories are deleted directly, and even those go through the
  guard if they turn out to contain a repository.
- **One unreadable worktree does not hide the others.** It is reported as held.
- **Every removal is logged** to `~/.config/warren/history.log` (`warren history`).

## How it works

1. Find repos under the configured roots and list their Claude worktrees.
2. Run the git checks in parallel: dirty tree, commits on no remote, ignored files,
   locks, and processes whose working directory is inside the worktree (via `lsof`).
3. Ask GitHub for the real PR state of each branch through `gh`. Most repos squash on
   merge, so a merged branch is never an ancestor of `main` and `git branch --merged`
   reports nothing. Answers are cached for six hours, and if GitHub is unreachable the
   last good answer is kept rather than forgotten.
4. Combine the results into one verdict per worktree. `clean` re-verifies each
   candidate, then removes it.

## Contributing

Issues and pull requests are welcome. Build and test with:

```sh
go build ./...
go test ./...
```

CI also runs `gofmt` and `go vet ./...`, so run those before opening a PR.

The suite builds a throwaway repo with one worktree per case (clean and pushed,
dirty, committed but on no remote, orphan, detached, locked, a gitignored `.env`, a
broken `.git` file, a process parked inside one) and asserts the verdicts and, above
all, that removal leaves every worktree holding work alone. It also covers the GitHub
path with a stubbed `gh`: merged, open and closed PRs, the per-branch fallback, and
that an offline `gh` keeps the stale cache instead of wiping it.

Tests never scan outside their fixture: every fixture writes an explicit paths file
and the test binary sets `WARREN_NO_DEFAULT_ROOTS`.

## License

MIT. See [LICENSE](LICENSE). Inspired by [mole](https://github.com/tw93/mole), but
shares no code with it.
