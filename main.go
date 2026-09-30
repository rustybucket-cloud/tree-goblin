package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

const usage = `tree-goblin — manage git worktrees

Usage: run inside a git repository.
  tree-goblin

Keys: ↑/↓ move, enter open, n new, d delete, c set open command,
      p prune, r refresh, q quit

The open command is stored in git config (` + configKey + `),
per repo or globally. Placeholders: {path} {branch} {name}.
With no command set, $SHELL is started in the worktree.
`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown argument %q\n\n%s", os.Args[1], usage)
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

	if _, err := tea.NewProgram(newModel(repo), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
