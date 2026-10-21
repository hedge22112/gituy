# gituy

An interactive git command builder. gituy reads your repository's state,
suggests the commands that make sense right now, explains every option, and
**always shows the exact git command it will run**. It's a layer on top of
git, not a replacement: everything it does is a plain git command you could
have typed yourself.

```
 MERGE IN PROGRESS  · main · ↑1 ↓0 · 1 conflicted · fetch paused during merge
──────────────────────────────────────────────────────────────────────────────
 Suggested
❯ Mark f.txt as resolved (after fixing it)
  Keep one side's version of the conflicted files
  Abort the merge ●
 All commands
  add                        Stage changes for the next commit
  ...
──────────────────────────────────────────────────────────────────────────────
$ git add -- f.txt
↑↓ move · → open · r run · c copy · / filter · f fetch · l log · q quit
```

## Features

- **State-aware suggestions**: merge/rebase/cherry-pick conflicts, ahead,
  behind or diverged from upstream, unpublished branches, staged and
  unstaged changes, stashes, detached HEAD.
- **Command builder**: toggle options with plain-English descriptions.
  Mutually exclusive flags are radio groups, so invalid combinations can't
  be built. Arguments come from pickers (branches, remotes, changed files,
  commits, stashes) with type-to-filter.
- **The command is always visible** at the bottom of the screen and updates
  as you change options. Press `c` to copy it, or `e` to edit it by hand:
  the edit is parsed back into the builder's toggles, and anything gituy
  doesn't recognise is passed through unchanged. On the home screen, `e`
  lets you type any git command.
- **Housekeeping**: suggests a safe `push --force-with-lease` after you
  rebase or amend pushed commits, cleaning up after a branch's remote was
  deleted, and deleting branches that are already merged.
- **Outside a repository** it offers `git init` and `git clone`, then
  carries on in the new repository.
- **Safety**: options that can lose work are shown in red and need
  confirmation.
- **Background tasks**: watches the working tree and `.git` for changes
  (with polling as a fallback) and fetches remotes periodically. Fetches
  never prompt for credentials, back off after failures, and pause during
  merges and rebases. Everything they do is shown in the activity log (`l`).
- Runs git attached to your terminal, so editors, pagers, hooks and
  credential prompts work normally. Afterwards you return to gituy.

## Install

Download a binary for Linux, macOS or Windows from the
[releases page](https://github.com/hedge22112/gituy/releases), or a `.deb`,
`.rpm` or `.apk` package. With Go:

```sh
go install github.com/hedge22112/gituy/cmd/gituy@latest
```

Requires git 2.30 or newer.

The macOS binaries aren't notarized yet: if macOS blocks it, run
`xattr -d com.apple.quarantine ./gituy`.

## Usage

```sh
gituy               # in any git repository
gituy -C path/to/repo
gituy --print       # print the chosen command instead of running it
gituy --no-fetch    # no background fetching this session
```

| Key | Home | Builder |
|---|---|---|
| `↑` `↓` / `j` `k` | move | move |
| `→` / `enter` | open the builder | toggle option / edit value / pick argument |
| `space` | | toggle |
| `x` | | clear an argument or value |
| `r` | run highlighted command | run |
| `c` | copy command | copy command |
| `e` | type a git command | edit the command line by hand |
| `/` | filter | |
| `f` | fetch now | |
| `l` | activity log | |
| `←` / `esc` | clear filter | back |
| `q` | quit | quit |

## Configuration

`~/.config/gituy/config.yaml` (or your OS's config directory), overridden
per repository by `.git/gituy.yaml`:

```yaml
fetch:
  enabled: true
  on_start: true
  interval: 5m        # 0 = only on start
  remotes: ["*"]      # or [origin, upstream]
  exclude: []
  prune: true
  tags: false
  timeout: 60s
status:
  watch: true               # react to file changes immediately
  max_watches: 8000         # bigger repos: watch .git only, poll the files
  poll_interval: 3s         # when not watching (0 = off)
  watch_poll_interval: 30s  # safety net while watching
return_after_run: true
max_suggestions: 6
```

## How it's built

| Package | Role |
|---|---|
| `internal/git` | runs git, parses `status --porcelain=v2`, detects in-progress operations |
| `internal/spec` | command definitions as YAML (`internal/spec/commands/*.yaml`) |
| `internal/builder` | spec + selections → argv, validation, danger level |
| `internal/rules` | suggestion rules as YAML (`internal/rules/rules.yaml`), conditions in [expr](https://expr-lang.org) |
| `internal/bg` | background file watcher, status polling and fetch |
| `internal/ui` | Bubble Tea screens: home, builder, picker, input, confirm, log |

### File watching

gituy watches `.git` (HEAD, index, refs, merge/rebase state) plus every
working-tree directory that holds a tracked or non-ignored file, so ignored
trees like `node_modules` cost nothing. New directories are picked up as
they appear. Changes are debounced (250ms), and lock files, editor swap
files and permission-only changes are ignored. Background `git status` runs
with `--no-optional-locks`, so refreshing never writes the index and can't
trigger itself.

If the repository is larger than `max_watches`, or the OS watch limit is
hit (Linux: `fs.inotify.max_user_watches`), gituy keeps watching `.git` and
polls the working tree instead. On macOS and BSD, files count towards the
limit too, because kqueue needs a file descriptor per file.

Adding a command or suggestion is usually a YAML change. See the comments
at the top of `rules.yaml` for what rule conditions can use.

```sh
go test -race ./...
```

## Commands

status, diff, add, restore, commit, reset, clean, fetch, pull, push,
switch, branch (create, rename, delete, list, unset upstream), merge,
rebase, cherry-pick, revert, checkout --ours/--theirs, log, stash (push,
pop, apply, list, drop), tag (create, delete, list), remote (add, remove,
list), worktree (add, list, remove), init, clone, plus continue/abort for
merge, rebase, cherry-pick and revert. Any other git command can be typed
with `e` and runs as typed.

## Releasing

Push a version tag; the release workflow builds every platform with
GoReleaser and publishes a GitHub release:

```sh
git tag v0.2.0 && git push origin v0.2.0
```

To also publish a Homebrew cask, create the `hedge22112/homebrew-tap`
repository and add a `HOMEBREW_TAP_TOKEN` secret (a token that can push to
it) to this repository. Until then that step is skipped.

## License

MIT
