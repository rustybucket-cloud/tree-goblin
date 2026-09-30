package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const configKey = "tree-goblin.openCommand"

type Worktree struct {
	Path     string
	Head     string
	Branch   string // short branch name; empty when detached or bare
	Detached bool
	Bare     bool
	Locked   bool
	Prunable bool
	Main     bool
	Dirty    int // number of changed files; -1 if unknown
}

// Repo describes the repository tree-goblin was launched from.
type Repo struct {
	Dir     string // directory git commands run in
	Current string // top level of the worktree we were launched from ("" if bare)
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func openRepo(dir string) (*Repo, error) {
	if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
		return nil, err
	}
	r := &Repo{Dir: dir}
	if top, err := git(dir, "rev-parse", "--show-toplevel"); err == nil {
		r.Current = realpath(top)
	}
	return r, nil
}

func realpath(p string) string {
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}
	return p
}

func (r *Repo) List() ([]Worktree, error) {
	out, err := git(r.Dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	wts := parseWorktrees(out)

	var wg sync.WaitGroup
	for i := range wts {
		wts[i].Dirty = -1
		if wts[i].Bare || wts[i].Prunable {
			continue
		}
		wg.Add(1)
		go func(wt *Worktree) {
			defer wg.Done()
			if s, err := git(wt.Path, "status", "--porcelain"); err == nil {
				wt.Dirty = countLines(s)
			}
		}(&wts[i])
	}
	wg.Wait()
	return wts, nil
}

func parseWorktrees(out string) []Worktree {
	var wts []Worktree
	var cur *Worktree
	for _, line := range strings.Split(out, "\n") {
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			wts = append(wts, Worktree{Path: val, Main: len(wts) == 0})
			cur = &wts[len(wts)-1]
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "detached":
			cur.Detached = true
		case "bare":
			cur.Bare = true
		case "locked":
			cur.Locked = true
		case "prunable":
			cur.Prunable = true
		}
	}
	return wts
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// Add creates a worktree at path for branch. An existing local branch is
// checked out; otherwise a new branch is created from base, from a matching
// remote branch, or from HEAD.
func (r *Repo) Add(path, branch, base string) error {
	if _, err := git(r.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err := git(r.Dir, "worktree", "add", path, branch)
		return err
	}
	if base == "" {
		if refs, _ := git(r.Dir, "for-each-ref", "--format=%(refname:short)", "refs/remotes/*/"+branch); refs != "" {
			base = strings.SplitN(refs, "\n", 2)[0]
		}
	}
	args := []string{"worktree", "add", "-b", branch, path}
	if base != "" {
		args = append(args, base)
	}
	_, err := git(r.Dir, args...)
	return err
}

func (r *Repo) Remove(path string, force bool) error {
	args := []string{"worktree", "remove", path}
	if force {
		args = []string{"worktree", "remove", "--force", "--force", path}
	}
	_, err := git(r.Dir, args...)
	return err
}

func (r *Repo) DeleteBranch(branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := git(r.Dir, "branch", flag, branch)
	return err
}

func (r *Repo) Prune() error {
	_, err := git(r.Dir, "worktree", "prune")
	return err
}

// OpenCommand returns the configured open command and the config scope it came from.
func (r *Repo) OpenCommand() (cmd, scope string) {
	out, err := git(r.Dir, "config", "--show-scope", "--get", configKey)
	if err != nil {
		return "", ""
	}
	scope, cmd, _ = strings.Cut(out, "\t")
	return cmd, scope
}

func (r *Repo) SetOpenCommand(cmd string, global bool) error {
	scope := "--local"
	if global {
		scope = "--global"
	}
	if cmd == "" {
		_, err := git(r.Dir, "config", scope, "--unset", configKey)
		if err != nil && !strings.Contains(err.Error(), "exit status 5") {
			return err
		}
		return nil
	}
	_, err := git(r.Dir, "config", scope, configKey, cmd)
	return err
}

// DefaultPath suggests <parent>/<repo>-worktrees/<branch> next to the main worktree.
func DefaultPath(mainPath, branch string) string {
	name := strings.TrimSuffix(filepath.Base(mainPath), ".git")
	dir := strings.NewReplacer("/", "-", " ", "-").Replace(branch)
	return filepath.Join(filepath.Dir(mainPath), name+"-worktrees", dir)
}

// ResolvePath expands ~ and makes relative paths relative to base.
func ResolvePath(p, base string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// OpenExec builds the process that "opens" a worktree. Placeholders {path},
// {branch} and {name} are substituted shell-quoted; with no command set, the
// user's $SHELL is started in the worktree.
func OpenExec(command string, wt Worktree) *exec.Cmd {
	var c *exec.Cmd
	if command == "" {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		c = exec.Command(shell)
	} else {
		s := strings.NewReplacer(
			"{path}", shellQuote(wt.Path),
			"{branch}", shellQuote(wt.Branch),
			"{name}", shellQuote(filepath.Base(wt.Path)),
		).Replace(command)
		c = exec.Command("/bin/sh", "-c", s)
	}
	c.Dir = wt.Path
	c.Env = append(os.Environ(),
		"TG_PATH="+wt.Path,
		"TG_BRANCH="+wt.Branch,
	)
	return c
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
