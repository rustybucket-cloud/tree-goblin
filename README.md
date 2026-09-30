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
tree-goblin prune                                  # prune stale entries
```

Aliases: `ls`, `add`, `remove`/`delete`. Errors exit 1; bad arguments exit 2.

## New worktrees

- Existing local branch → checked out.
- New branch → created from **Base**, or from a matching remote branch (`origin/<name>`), or from `HEAD`.
- Default path: `../<repo>-worktrees/<branch>` (slashes become `-`). Editable.

## Open command

Stored in git config as `tree-goblin.openCommand`, per repo (default) or globally (`tab` in the dialog).
It runs via `sh -c` inside the worktree directory; tree-goblin resumes when it exits.

- Placeholders (shell-quoted): `{path}`, `{branch}`, `{name}`
- Env vars: `$TG_PATH`, `$TG_BRANCH`
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
