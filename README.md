# tree-goblin 🌳

A TUI for managing git worktrees. Run it from inside any repo.

## Install

```sh
go install github.com/rustybucket-cloud/tree-goblin@latest   # installs to ~/go/bin (must be on PATH)
```

Or from a clone: `go install .`

## Keys

| Key       | Action                                             |
|-----------|----------------------------------------------------|
| `↑↓` `jk` | move                                               |
| `enter`   | open the worktree (runs the open command)          |
| `n`       | new worktree (branch, optional base, path)         |
| `d`       | delete worktree (`b` in the prompt also deletes the branch) |
| `c`       | set the open command                               |
| `h`       | set the post-create hook                           |
| `l`       | replace external symlinks with clones (see below)  |
| `p`       | prune stale worktree entries                       |
| `r`       | refresh                                            |
| `q`       | quit                                               |

## CLI

Every TUI action is also available as a subcommand (no TUI is opened).
`<worktree>` is a branch name, path, or worktree directory name.

```sh
tree-goblin list [--json]                          # list worktrees
tree-goblin new <branch> [--base <ref>] [--path <dir>]  # create; prints the path
tree-goblin rm <worktree> [-b|--branch] [-f|--force]    # remove (-b also deletes branch)
tree-goblin open <worktree>                        # run the open command
tree-goblin path <worktree>                        # print path, e.g. cd "$(tree-goblin path foo)"
tree-goblin config [<command>] [--global] [--unset]     # show/set the open command
tree-goblin config --post-create [<command>] [--global] [--unset]  # show/set the post-create hook
tree-goblin fix-links <worktree>                   # replace external symlinks with clones
tree-goblin prune                                  # prune stale entries
```

Aliases: `ls`, `add`, `remove`/`delete`. Errors exit 1; bad arguments exit 2.

## New worktrees

- Existing local branch → checked out.
- New branch → created from **Base**, or from a matching remote branch (`origin/<name>`), or from `HEAD`.
- Default path: `../<repo>-worktrees/<branch>` (slashes become `-`). Editable.
- Paths listed in `.worktreeinclude` are copied in, then the post-create hook runs.

### `.worktreeinclude`

Put this in the main worktree to copy gitignored files and folders into every new worktree.
List one path or glob per line, relative to the repo root. Lines starting with `#` are comments.

```
client/node_modules
client/.env
client/android/local.properties
```

Copies use copy-on-write clones where the filesystem supports them (`cp -c` on APFS, `--reflink` on btrfs/XFS).
So even a big `node_modules` copies in seconds and uses almost no extra disk.
Each copy is a real directory, so bundlers like Metro work, and each worktree can change its dependencies without affecting the others.
Paths that already exist in the new worktree are skipped.

### Post-create hook

Stored in git config as `tree-goblin.postCreate`.
It runs via `sh -c` in the new worktree and uses the same placeholders as the open command, plus `{main}` / `$TG_MAIN` (the main worktree).
Output goes to stderr in CLI mode.
If the hook fails, the worktree is kept and the error is reported.

```sh
tree-goblin config --post-create 'npm ci --prefix client'
```

### External symlinks

`list` and the TUI flag ignored symlinks that resolve outside their worktree (e.g. a `node_modules` symlinked to the main checkout).
Metro and similar tools follow these symlinks out of the project and break.
`tree-goblin fix-links <worktree>` (or `l` in the TUI) replaces each one with a clone of its target.

## Open command

Stored in git config as `tree-goblin.openCommand`, per repo (default) or globally (`tab` in the dialog).
It runs via `sh -c` inside the worktree directory; tree-goblin resumes when it exits.

- Placeholders (shell-quoted): `{path}`, `{branch}`, `{name}`, `{main}`
- Env vars: `$TG_PATH`, `$TG_BRANCH`, `$TG_MAIN`
- Unset → starts `$SHELL` in the worktree

Examples:

```sh
code {path}
nvim .
claude
tmux new-window -c {path} -n {name}
```

## License

MIT
