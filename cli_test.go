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
	if c, _ := r.OpenCommand(); c != "" {
		t.Fatalf("expected unset, got %q", c)
	}
}
