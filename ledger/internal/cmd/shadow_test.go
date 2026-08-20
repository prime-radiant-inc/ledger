package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ledger/internal/gitx"
	"ledger/internal/store"
)

// shadowedLayout reproduces the eval's silent-shadowing layout: a bare store
// one level above a project repo, created by a misplaced `chit init` and
// holding the only real ledger, while the repo has a store of its own that
// is empty. Leaves the test standing inside the repo with ambient
// resolution in force, and returns the repo dir and the bare store's path.
func shadowedLayout(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if so, se, code := run(t, root, "init"); code != 0 {
		t.Fatalf("init bare store: %s %s", so, se)
	}
	bare := filepath.Join(root, ".ledger.git")
	if so, se, code := run(t, bare, "create", "gateway-502", "--scope", "investigation"); code != 0 {
		t.Fatalf("create in bare store: %s %s", so, se)
	}
	repo := filepath.Join(root, "proj")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepoAt(t, repo)
	t.Setenv("LEDGER_DIR", "")
	t.Chdir(repo)
	return repo, bare
}

// ambient runs the CLI with no --store, so store resolution walks the
// ancestry the way it does for a real agent standing in a project.
func ambient(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var so, se bytes.Buffer
	code := ExecuteArgs(args, &so, &se)
	return so.String(), se.String(), code
}

// TestLsNamesShadowedAncestorStore: `ls` inside the project sees an empty
// store and used to say only "no ledgers", with `ls --all` dead-ending in
// the same place — the ancestor store holding the actual work was never
// mentioned anywhere. It is now named in the listing itself.
func TestLsNamesShadowedAncestorStore(t *testing.T) {
	_, bare := shadowedLayout(t)

	for _, args := range [][]string{{"ls"}, {"ls", "--all"}} {
		so, se, code := ambient(t, args...)
		if code != 0 {
			t.Fatalf("%v: %s", args, se)
		}
		doc := mustJSON(t, so)
		if len(doc["ledgers"].([]any)) != 0 {
			t.Fatalf("%v must still list the repo's own (empty) store: %v", args, doc)
		}
		if doc["shadowed_store"] != bare {
			t.Fatalf("%v must name the shadowed store %q: %v", args, bare, doc)
		}
	}

	// a store with no shadowed ancestor carries no such field
	so, _, _ := run(t, initRepo(t), "ls")
	if _, ok := mustJSON(t, so)["shadowed_store"]; ok {
		t.Fatal("shadowed_store must be absent when nothing is shadowed")
	}
}

// TestLsTTYNamesShadowedStore: the same breadcrumb on a terminal, as a
// trailing note under the listing.
func TestLsTTYNamesShadowedStore(t *testing.T) {
	dir := initRepo(t)
	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf,
		Shadowed: "/work/.ledger.git"}
	if err := runLs(c, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "--store /work/.ledger.git") {
		t.Fatalf("TTY listing must name the other store: %q", buf.String())
	}
}

// TestUnknownLedgerAndNoOpenLedgerHintsNameShadowedStore: both dead ends the
// eval hit — asking for a ledger that lives in the other store, and asking
// for anything at all in a store with nothing in it — now point at the
// store that has the events, with the flag that reaches it.
func TestUnknownLedgerAndNoOpenLedgerHintsNameShadowedStore(t *testing.T) {
	_, bare := shadowedLayout(t)

	_, se, code := ambient(t, "show", "--ledger", "gateway-502")
	if code != 4 || !strings.Contains(se, "unknown_ledger") {
		t.Fatalf("unknown_ledger expected: %d %s", code, se)
	}
	if !strings.Contains(se, "--store "+bare) {
		t.Fatalf("unknown_ledger hint must name the other store: %s", se)
	}

	_, se, code = ambient(t, "status")
	if code != 4 || !strings.Contains(se, "no_open_ledger") {
		t.Fatalf("no_open_ledger expected: %d %s", code, se)
	}
	if !strings.Contains(se, "--store "+bare) {
		t.Fatalf("no_open_ledger hint must name the other store: %s", se)
	}

	// and the pasted fix actually works
	so, se, code := ambient(t, "--store", bare, "show", "--ledger", "gateway-502")
	if code != 0 {
		t.Fatalf("the hinted command must read the other store: %s %s", so, se)
	}
	if mustJSON(t, so)["ledger"] != "gateway-502" {
		t.Fatalf("wrong ledger: %s", so)
	}
}
