package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

const usage = `tree-goblin — manage git worktrees

Usage: run inside a git repository.
  tree-goblin                      open the TUI
  tree-goblin list [--json]        list worktrees
  tree-goblin new <branch> [--base <ref>] [--path <dir>]
                                   create a worktree; prints its path
  tree-goblin rm <worktree> [-b] [-f]
                                   remove a worktree (-b also deletes the
                                   branch, -f forces both)
  tree-goblin open <worktree>      run the open command in a worktree
  tree-goblin path <worktree>      print a worktree's path
  tree-goblin config [--post-create] [<command>] [--global] [--unset]
                                   show or set the open command (or the
                                   post-create hook)
  tree-goblin fix-links <worktree> replace ignored symlinks pointing outside
                                   the worktree with copy-on-write clones
  tree-goblin prune                prune stale worktree entries

<worktree> is a branch name, path, or worktree directory name.

TUI keys: ↑/↓ move, enter open, n new, d delete, c set open command,
          h set post-create hook, l fix links, p prune, r refresh, q quit

The open command (` + openKey + `) and post-create hook
(` + postCreateKey + `) are stored in git config, per repo or globally.
Placeholders: {path} {branch} {name} {main}. With no open command set,
$SHELL is started in the worktree.

New worktrees get copy-on-write clones of the paths listed in the main
worktree's ` + includeFile + ` (one path or glob per line), then the
post-create hook runs inside the new worktree.
`

func main() {
	var sub string
	if len(os.Args) > 1 {
		sub = os.Args[1]
		switch sub {
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		}
		if _, ok := commands[sub]; !ok {
			fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", sub, usage)
			os.Exit(2)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	repo, err := openRepo(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tree-goblin: not a git repository")
		os.Exit(1)
	}

	if sub != "" {
		c := &cli{repo: repo, cwd: cwd, out: os.Stdout, err: os.Stderr}
		os.Exit(c.run(sub, os.Args[2:]))
	}

	if _, err := tea.NewProgram(newModel(repo), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
