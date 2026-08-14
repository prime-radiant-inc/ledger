package gitx

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestGitRunsInDir(t *testing.T) {
	dir := initRepo(t)
	r := Repo{Dir: dir}
	out, stderr, code := r.Git("", "rev-parse", "--is-inside-work-tree")
	if code != 0 || out != "true" {
		t.Fatalf("got %q %q %d", out, stderr, code)
	}
}

func TestGitStdinAndFailure(t *testing.T) {
	dir := initRepo(t)
	r := Repo{Dir: dir}
	blob, _, code := r.Git("hello", "hash-object", "-w", "--stdin")
	if code != 0 || len(blob) != 40 {
		t.Fatalf("hash-object: %q %d", blob, code)
	}
	_, stderr, code := r.Git("", "rev-parse", "-q", "--verify", "refs/nope")
	if code == 0 {
		t.Fatalf("expected nonzero, stderr=%q", stderr)
	}
}

func TestCheckVersion(t *testing.T) {
	if err := CheckVersion(); err != nil {
		t.Fatalf("system git should satisfy the floor: %v", err)
	}
}

func TestIdentityArgs(t *testing.T) {
	got := strings.Join(IdentityArgs("alice", "terminal"), " ")
	want := "-c user.name=alice -c user.email=author@ledger.invalid -c committer.name=terminal -c committer.email=marker@ledger.invalid"
	if got != want {
		t.Fatalf("got %q", got)
	}
	_ = os.Environ // keep imports honest if edited
}
