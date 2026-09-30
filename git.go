package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	openKey       = "tree-goblin.openCommand"
	postCreateKey = "tree-goblin.postCreate"
	includeFile   = ".worktreeinclude"
)

type Worktree struct {
	Path     string
	Head     string
	Branch   string // short branch name; empty when detached or bare
	Detached bool
	Bare     bool
	Locked   bool
	Prunable bool
	Main     bool
	Dirty    int      // number of changed files; -1 if unknown
	Links    []string // ignored symlinks (relative) that resolve outside the worktree
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
			if s, err := git(wt.Path, "status", "--porcelain", "--ignored", "-z"); err == nil {
				var ignored []string
				wt.Dirty, ignored = parseStatus(s)
				wt.Links = externalLinks(wt.Path, ignored)
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

// parseStatus parses `git status --porcelain --ignored -z` into the number of
// changed files and the ignored paths.
func parseStatus(out string) (dirty int, ignored []string) {
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		switch xy := e[:2]; {
		case xy == "!!":
			ignored = append(ignored, e[3:])
		default:
			dirty++
			if xy[0] == 'R' || xy[0] == 'C' {
				i++ // skip the rename/copy source
			}
		}
	}
	return dirty, ignored
}

// externalLinks returns the paths in rels that are symlinks resolving outside dir.
func externalLinks(dir string, rels []string) []string {
	root := realpath(dir)
	var links []string
	for _, rel := range rels {
		p := filepath.Join(dir, rel)
		if fi, err := os.Lstat(p); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		target, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		if r, err := filepath.Rel(root, target); err != nil || !filepath.IsLocal(r) {
			links = append(links, rel)
		}
	}
	return links
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

// Config returns a tree-goblin setting and the config scope it came from.
func (r *Repo) Config(key string) (val, scope string) {
	out, err := git(r.Dir, "config", "--show-scope", "--get", key)
	if err != nil {
		return "", ""
	}
	scope, val, _ = strings.Cut(out, "\t")
	return val, scope
}

// SetConfig stores a setting in the repo (or global) git config; an empty
// value unsets it.
func (r *Repo) SetConfig(key, val string, global bool) error {
	scope := "--local"
	if global {
		scope = "--global"
	}
	if val == "" {
		_, err := git(r.Dir, "config", scope, "--unset", key)
		if err != nil && !strings.Contains(err.Error(), "exit status 5") {
			return err
		}
		return nil
	}
	_, err := git(r.Dir, "config", scope, key, val)
	return err
}

// SourcePath is the checkout new worktrees copy files from: the main
// worktree, or the current one when the main worktree is bare.
func (r *Repo) SourcePath(wts []Worktree) string {
	if len(wts) > 0 && !wts[0].Bare {
		return wts[0].Path
	}
	return r.Current
}

// Setup prepares a freshly created worktree: it clones the paths listed in
// src/.worktreeinclude into it, then runs the post-create hook with its
// output sent to out. It returns the paths it copied.
func (r *Repo) Setup(wt Worktree, src string, out io.Writer) ([]string, error) {
	var copied []string
	var errs []error
	if src != "" {
		rels, err := Includes(src)
		errs = append(errs, err)
		for _, rel := range rels {
			dst := filepath.Join(wt.Path, rel)
			if _, err := os.Lstat(dst); err == nil {
				continue // tracked, or already there
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := cloneCopy(filepath.Join(src, rel), dst); err != nil {
				errs = append(errs, fmt.Errorf("copy %s: %w", rel, err))
				continue
			}
			copied = append(copied, rel)
		}
	}
	if hook, _ := r.Config(postCreateKey); hook != "" {
		c := shellExec(hook, wt, src)
		c.Stdout, c.Stderr = out, out
		if err := c.Run(); err != nil {
			errs = append(errs, fmt.Errorf("post-create: %w", err))
		}
	}
	return copied, errors.Join(errs...)
}

// Includes returns the paths (relative to src) matched by the lines of
// src/.worktreeinclude. Lines are paths or globs; # starts a comment.
func Includes(src string) ([]string, error) {
	f, err := os.Open(filepath.Join(src, includeFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()

	seen := map[string]bool{}
	var rels []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.Trim(strings.TrimSpace(sc.Text()), "/")
		if line == "" || strings.HasPrefix(line, "#") || !filepath.IsLocal(line) {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(src, filepath.FromSlash(line)))
		for _, m := range matches {
			if rel, err := filepath.Rel(src, m); err == nil && !seen[rel] && rel != includeFile {
				seen[rel] = true
				rels = append(rels, rel)
			}
		}
	}
	return rels, sc.Err()
}

// FixLink replaces the symlink dir/rel with a copy-on-write clone of its target.
func FixLink(dir, rel string) error {
	link := filepath.Join(dir, rel)
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return err
	}
	tmp := link + ".tree-goblin-tmp"
	if _, err := os.Lstat(tmp); err == nil {
		return fmt.Errorf("%s already exists", tmp)
	}
	if err := cloneCopy(target, tmp); err != nil {
		return err
	}
	if err := os.Remove(link); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return os.Rename(tmp, link)
}

// cloneCopy recursively copies src to dst, which must not exist, using
// copy-on-write clones where the filesystem supports them (APFS, btrfs, …).
func cloneCopy(src, dst string) error {
	attempts := [][]string{{"-R", "--reflink=auto"}, {"-R"}}
	if runtime.GOOS == "darwin" {
		attempts = [][]string{{"-cR"}, {"-R"}}
	}
	var err error
	for _, flags := range attempts {
		out, e := exec.Command("cp", append(flags, src, dst)...).CombinedOutput()
		if e == nil {
			return nil
		}
		os.RemoveAll(dst) // drop any partial copy before retrying
		if msg := strings.TrimSpace(string(out)); msg != "" {
			e = errors.New(msg)
		}
		err = e
	}
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

// OpenExec builds the process that "opens" a worktree. With no command set,
// the user's $SHELL is started in the worktree.
func OpenExec(command string, wt Worktree, mainPath string) *exec.Cmd {
	if command != "" {
		return shellExec(command, wt, mainPath)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	c := exec.Command(shell)
	c.Dir = wt.Path
	c.Env = append(os.Environ(), "TG_PATH="+wt.Path, "TG_BRANCH="+wt.Branch, "TG_MAIN="+mainPath)
	return c
}

// shellExec runs command via sh -c in the worktree. Placeholders {path},
// {branch}, {name} and {main} are substituted shell-quoted.
func shellExec(command string, wt Worktree, mainPath string) *exec.Cmd {
	s := strings.NewReplacer(
		"{path}", shellQuote(wt.Path),
		"{branch}", shellQuote(wt.Branch),
		"{name}", shellQuote(filepath.Base(wt.Path)),
		"{main}", shellQuote(mainPath),
	).Replace(command)
	c := exec.Command("/bin/sh", "-c", s)
	c.Dir = wt.Path
	c.Env = append(os.Environ(), "TG_PATH="+wt.Path, "TG_BRANCH="+wt.Branch, "TG_MAIN="+mainPath)
	return c
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
