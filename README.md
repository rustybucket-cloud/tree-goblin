# tree-goblin 🌳

A TUI for managing git worktrees. Run it from inside any repo.

## Install

```sh
go install .        # or: go build -o ~/bin/tree-goblin .
```

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
