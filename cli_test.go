package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func runCLI(t *testing.T, r *Repo, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := &cli{repo: r, cwd: r.Current, out: &out, err: &errOut}
	code := c.run(args[0], args[1:])
	return out.String(), errOut.String(), code
}

func TestParseInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	f := fs.Bool("f", false, "")
	pos, err := parse(fs, []string{"a", "-f", "b", "--", "-c"})
	if err != nil || !*f || !reflect.DeepEqual(pos, []string{"a", "b", "-c"}) {
		t.Fatalf("got %v %v %v", pos, *f, err)
	}
}

func TestCLILifecycle(t *testing.T) {
	r := setupRepo(t)

	out, _, code := runCLI(t, r, "new", "feat/y")
	want := DefaultPath(r.Current, "feat/y")
	if code != 0 || strings.TrimSpace(out) != want {
		t.Fatalf("new: %d %q", code, out)
	}

	out, _, _ = runCLI(t, r, "list", "--json")
	var wts []worktreeJSON
	if err := json.Unmarshal([]byte(out), &wts); err != nil || len(wts) != 2 || !wts[0].Current || wts[1].Branch != "feat/y" {
		t.Fatalf("list: %v %s", err, out)
	}

	for _, arg := range []string{"feat/y", "feat-y", want} {
		if out, _, _ := runCLI(t, r, "path", arg); strings.TrimSpace(out) != want {
			t.Fatalf("path %s: %q", arg, out)
		}
	}

	if _, _, code := runCLI(t, r, "rm", "main"); code != 1 {
		t.Fatal("expected refusing to remove main worktree")
	}
	os.WriteFile(filepath.Join(want, "f"), []byte("x"), 0o644)
	if _, stderr, code := runCLI(t, r, "rm", "feat/y"); code != 1 || !strings.Contains(stderr, "--force") {
		t.Fatalf("expected dirty remove to fail: %s", stderr)
	}
	if _, stderr, code := runCLI(t, r, "rm", "feat/y", "-b", "-f"); code != 0 {
		t.Fatalf("rm -b -f: %s", stderr)
	}
	if _, err := git(r.Current, "rev-parse", "--verify", "refs/heads/feat/y"); err == nil {
		t.Fatal("expected branch to be deleted")
	}
}

func TestCLIConfig(t *testing.T) {
	r := setupRepo(t)
	runCLI(t, r, "config", "code", "{path}")
	if out, _, _ := runCLI(t, r, "config"); out != "code {path}\t(local)\n" {
		t.Fatalf("got %q", out)
	}
	runCLI(t, r, "config", "--unset")
	if c, _ := r.Config(openKey); c != "" {
		t.Fatalf("expected unset, got %q", c)
	}
}

func TestCLISetupAndFixLinks(t *testing.T) {
	r := setupRepo(t)
	main := r.Current
	os.WriteFile(filepath.Join(main, ".gitignore"), []byte("node_modules\n.env\n"), 0o644)
	os.WriteFile(filepath.Join(main, ".worktreeinclude"), []byte("# deps\nnode_modules/\n.env\nmissing\n"), 0o644)
	os.MkdirAll(filepath.Join(main, "node_modules", "pkg"), 0o755)
	os.WriteFile(filepath.Join(main, "node_modules", "pkg", "index.js"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(main, ".env"), []byte("A=1"), 0o644)
	git(main, "add", ".gitignore", ".worktreeinclude")
	git(main, "commit", "-qm", "ignore")

	runCLI(t, r, "config", "--post-create", "echo", "{branch}", ">", "hook.txt")
	if out, _, _ := runCLI(t, r, "config", "--post-create"); !strings.HasPrefix(out, "echo {branch} > hook.txt") {
		t.Fatalf("config: %q", out)
	}

	out, stderr, code := runCLI(t, r, "new", "feat/z")
	wt := strings.TrimSpace(out)
	if code != 0 {
		t.Fatalf("new: %s", stderr)
	}
	if fi, err := os.Lstat(filepath.Join(wt, "node_modules")); err != nil || !fi.IsDir() {
		t.Fatalf("node_modules not cloned: %v", err)
	}
	for f, want := range map[string]string{"node_modules/pkg/index.js": "x", ".env": "A=1", "hook.txt": "feat/z\n"} {
		if b, _ := os.ReadFile(filepath.Join(wt, f)); string(b) != want {
			t.Fatalf("%s = %q", f, b)
		}
	}

	// a symlinked node_modules pointing back at main is flagged and fixable
	os.RemoveAll(filepath.Join(wt, "node_modules"))
	os.Symlink(filepath.Join(main, "node_modules"), filepath.Join(wt, "node_modules"))
	out, _, _ = runCLI(t, r, "list", "--json")
	var wts []worktreeJSON
	json.Unmarshal([]byte(out), &wts)
	if len(wts) != 2 || !reflect.DeepEqual(wts[1].Links, []string{"node_modules"}) || wts[0].Links != nil {
		t.Fatalf("links: %s", out)
	}
	if _, stderr, code := runCLI(t, r, "fix-links", "feat/z"); code != 0 {
		t.Fatalf("fix-links: %s", stderr)
	}
	if fi, err := os.Lstat(filepath.Join(wt, "node_modules")); err != nil || !fi.IsDir() {
		t.Fatalf("still a symlink: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "node_modules", "pkg", "index.js")); string(b) != "x" {
		t.Fatal("clone missing contents")
	}
}
