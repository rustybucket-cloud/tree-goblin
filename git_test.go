package main

import (
	"os"
	"path/filepath"
	"testing"
)

func setupRepo(t *testing.T) *Repo {
	t.Helper()
	dir := realpath(t.TempDir())
	main := filepath.Join(dir, "proj")
	os.MkdirAll(main, 0o755)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "gitconfig"))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if _, err := git(main, args...); err != nil {
			t.Fatal(err)
		}
	}
	r, err := openRepo(main)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLifecycle(t *testing.T) {
	r := setupRepo(t)
	path := DefaultPath(r.Current, "feat/x")
	if filepath.Base(filepath.Dir(path)) != "proj-worktrees" || filepath.Base(path) != "feat-x" {
		t.Fatalf("unexpected default path %s", path)
	}
	if err := r.Add(path, "feat/x", ""); err != nil {
		t.Fatal(err)
	}
	wts, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 || !wts[0].Main || wts[1].Branch != "feat/x" || wts[1].Dirty != 0 {
		t.Fatalf("unexpected list: %+v", wts)
	}

	// dirty worktree needs force
	os.WriteFile(filepath.Join(path, "f"), []byte("x"), 0o644)
	if err := r.Remove(path, false); err == nil {
		t.Fatal("expected remove of dirty worktree to fail")
	}
	if err := r.Remove(path, true); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteBranch("feat/x", false); err != nil {
		t.Fatal(err)
	}

	// existing branch is checked out rather than created
	git(r.Current, "branch", "existing")
	if err := r.Add(DefaultPath(r.Current, "existing"), "existing", ""); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCommandConfig(t *testing.T) {
	r := setupRepo(t)
	if c, _ := r.OpenCommand(); c != "" {
		t.Fatalf("expected empty, got %q", c)
	}
	r.SetOpenCommand("code {path}", true)
	r.SetOpenCommand("nvim .", false)
	if c, s := r.OpenCommand(); c != "nvim ." || s != "local" {
		t.Fatalf("got %q %q", c, s)
	}
	r.SetOpenCommand("", false)
	if c, s := r.OpenCommand(); c != "code {path}" || s != "global" {
		t.Fatalf("got %q %q", c, s)
	}
}

func TestOpenExec(t *testing.T) {
	c := OpenExec("echo {path} {branch}", Worktree{Path: "/a b/it's", Branch: "x"})
	if got := c.Args[2]; got != `echo '/a b/it'\''s' 'x'` {
		t.Fatalf("got %s", got)
	}
}
