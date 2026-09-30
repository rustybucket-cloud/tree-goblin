package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// errUsage marks errors caused by bad arguments (exit status 2).
var errUsage = errors.New("usage")

type cli struct {
	repo *Repo
	cwd  string
	out  io.Writer
	err  io.Writer
}

var commands = map[string]func(*cli, []string) error{
	"list":      (*cli).list,
	"ls":        (*cli).list,
	"new":       (*cli).add,
	"add":       (*cli).add,
	"rm":        (*cli).remove,
	"remove":    (*cli).remove,
	"delete":    (*cli).remove,
	"open":      (*cli).open,
	"path":      (*cli).path,
	"config":    (*cli).config,
	"prune":     (*cli).prune,
	"fix-links": (*cli).fixLinks,
}

// run executes a CLI subcommand and returns the process exit status.
func (c *cli) run(name string, args []string) int {
	err := commands[name](c, args)
	var exit exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.code
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintf(c.err, "tree-goblin %s: %v\n", name, strings.TrimPrefix(err.Error(), "usage: "))
		return 2
	default:
		fmt.Fprintf(c.err, "tree-goblin %s: %v\n", name, err)
		return 1
	}
}

type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func usageErr(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errUsage}, a...)...)
}

func (c *cli) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.err)
	return fs
}

// parse parses flags that may be interspersed with positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %v", errUsage, err)
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		// flag consumed a "--" terminator: everything after it is positional.
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

type worktreeJSON struct {
	Path     string   `json:"path"`
	Branch   string   `json:"branch"`
	Head     string   `json:"head"`
	Main     bool     `json:"main"`
	Current  bool     `json:"current"`
	Detached bool     `json:"detached"`
	Bare     bool     `json:"bare"`
	Locked   bool     `json:"locked"`
	Prunable bool     `json:"prunable"`
	Dirty    int      `json:"dirty"`
	Links    []string `json:"links"`
}

func (c *cli) isCurrent(wt Worktree) bool {
	return c.repo.Current != "" && realpath(wt.Path) == c.repo.Current
}

func (c *cli) list(args []string) error {
	fs := c.flags("list")
	asJSON := fs.Bool("json", false, "output JSON")
	if pos, err := parse(fs, args); err != nil {
		return err
	} else if len(pos) > 0 {
		return usageErr("unexpected argument %q", pos[0])
	}
	wts, err := c.repo.List()
	if err != nil {
		return err
	}

	if *asJSON {
		out := make([]worktreeJSON, len(wts))
		for i, wt := range wts {
			out[i] = worktreeJSON{wt.Path, wt.Branch, wt.Head, wt.Main, c.isCurrent(wt),
				wt.Detached, wt.Bare, wt.Locked, wt.Prunable, wt.Dirty, wt.Links}
		}
		enc := json.NewEncoder(c.out)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	tw := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	for _, wt := range wts {
		var f []string
		if c.isCurrent(wt) {
			f = append(f, "here")
		}
		if wt.Main {
			f = append(f, "main")
		}
		if wt.Dirty > 0 {
			f = append(f, fmt.Sprintf("+%d", wt.Dirty))
		}
		if wt.Locked {
			f = append(f, "locked")
		}
		if wt.Prunable {
			f = append(f, "missing")
		}
		if len(wt.Links) > 0 {
			f = append(f, fmt.Sprintf("links:%d", len(wt.Links)))
		}
		head := wt.Head
		if len(head) > 7 {
			head = head[:7]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", branchLabel(wt), strings.Join(f, ","), head, wt.Path)
	}
	return tw.Flush()
}

func (c *cli) add(args []string) error {
	fs := c.flags("new")
	base := fs.String("base", "", "base ref for a new branch (default: matching remote branch, or HEAD)")
	path := fs.String("path", "", "worktree path (default: ../<repo>-worktrees/<branch>)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("expected a branch name")
	}
	branch := pos[0]

	wts, err := c.repo.List()
	if err != nil {
		return err
	}
	mainPath := c.repo.Current
	if len(wts) > 0 {
		mainPath = wts[0].Path
	}
	p := DefaultPath(mainPath, branch)
	if *path != "" {
		p = ResolvePath(*path, c.cwd)
	}
	if err := c.repo.Add(p, branch, *base); err != nil {
		return err
	}
	fmt.Fprintln(c.out, p)
	copied, err := c.repo.Setup(Worktree{Path: p, Branch: branch}, c.repo.SourcePath(wts), c.err)
	for _, rel := range copied {
		fmt.Fprintln(c.err, "Copied", rel)
	}
	return err
}

func (c *cli) remove(args []string) error {
	fs := c.flags("rm")
	delBranch := fs.Bool("branch", false, "also delete the worktree's branch")
	fs.BoolVar(delBranch, "b", false, "shorthand for -branch")
	force := fs.Bool("force", false, "remove even with uncommitted changes; delete unmerged branch")
	fs.BoolVar(force, "f", false, "shorthand for -force")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("expected a worktree (branch, path or directory name)")
	}
	wt, err := c.find(pos[0])
	if err != nil {
		return err
	}
	switch {
	case wt.Main:
		return errors.New("can't remove the main worktree")
	case c.isCurrent(wt):
		return errors.New("can't remove the worktree you're currently in")
	}

	if err := c.repo.Remove(wt.Path, *force); err != nil {
		if !*force {
			return fmt.Errorf("%v (use --force to remove anyway)", err)
		}
		return err
	}
	fmt.Fprintln(c.err, "Removed", wt.Path)
	if *delBranch && wt.Branch != "" {
		if err := c.repo.DeleteBranch(wt.Branch, *force); err != nil {
			if !*force {
				return fmt.Errorf("%v (use --force to delete anyway)", err)
			}
			return err
		}
		fmt.Fprintln(c.err, "Deleted branch", wt.Branch)
	}
	return nil
}

func (c *cli) open(args []string) error {
	fs := c.flags("open")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("expected a worktree (branch, path or directory name)")
	}
	wt, err := c.find(pos[0])
	if err != nil {
		return err
	}
	if wt.Prunable || wt.Bare {
		return errors.New("can't open a bare or missing worktree")
	}
	cmd, _ := c.repo.Config(openKey)
	wts, _ := c.repo.List()
	proc := OpenExec(cmd, wt, c.repo.SourcePath(wts))
	proc.Stdin, proc.Stdout, proc.Stderr = os.Stdin, c.out, c.err
	if err := proc.Run(); err != nil {
		if proc.ProcessState != nil {
			return exitError{proc.ProcessState.ExitCode()}
		}
		return err
	}
	return nil
}

func (c *cli) path(args []string) error {
	fs := c.flags("path")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("expected a worktree (branch, path or directory name)")
	}
	wt, err := c.find(pos[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, wt.Path)
	return nil
}

func (c *cli) config(args []string) error {
	fs := c.flags("config")
	global := fs.Bool("global", false, "write to global git config instead of the repo")
	unset := fs.Bool("unset", false, "clear the command")
	postCreate := fs.Bool("post-create", false, "show/set the post-create hook instead of the open command")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	key := openKey
	if *postCreate {
		key = postCreateKey
	}
	switch {
	case *unset:
		if len(pos) > 0 {
			return usageErr("--unset takes no command")
		}
		return c.repo.SetConfig(key, "", *global)
	case len(pos) > 0:
		return c.repo.SetConfig(key, strings.Join(pos, " "), *global)
	}
	cmd, scope := c.repo.Config(key)
	if cmd == "" {
		if *postCreate {
			fmt.Fprintln(c.err, "no post-create hook set")
		} else {
			fmt.Fprintln(c.err, "no open command set ($SHELL is used)")
		}
		return nil
	}
	fmt.Fprintf(c.out, "%s\t(%s)\n", cmd, scope)
	return nil
}

func (c *cli) prune(args []string) error {
	fs := c.flags("prune")
	if pos, err := parse(fs, args); err != nil {
		return err
	} else if len(pos) > 0 {
		return usageErr("unexpected argument %q", pos[0])
	}
	return c.repo.Prune()
}

func (c *cli) fixLinks(args []string) error {
	fs := c.flags("fix-links")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("expected a worktree (branch, path or directory name)")
	}
	wt, err := c.find(pos[0])
	if err != nil {
		return err
	}
	if len(wt.Links) == 0 {
		fmt.Fprintln(c.err, "no symlinks point outside", wt.Path)
		return nil
	}
	var errs []error
	for _, rel := range wt.Links {
		if err := FixLink(wt.Path, rel); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		fmt.Fprintln(c.err, "Cloned", rel)
	}
	return errors.Join(errs...)
}

// find resolves arg to a worktree by branch name, path, or directory name.
func (c *cli) find(arg string) (Worktree, error) {
	wts, err := c.repo.List()
	if err != nil {
		return Worktree{}, err
	}
	for _, wt := range wts {
		if wt.Branch == arg {
			return wt, nil
		}
	}
	p := realpath(ResolvePath(arg, c.cwd))
	for _, wt := range wts {
		if realpath(wt.Path) == p {
			return wt, nil
		}
	}
	var matches []Worktree
	for _, wt := range wts {
		if filepath.Base(wt.Path) == arg {
			matches = append(matches, wt)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Worktree{}, fmt.Errorf("no worktree matches %q", arg)
	default:
		return Worktree{}, fmt.Errorf("%q is ambiguous; use a branch name or path", arg)
	}
}
