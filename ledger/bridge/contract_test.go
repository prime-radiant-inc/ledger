package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ledger/bridge/fakegh"
)

// The command's own contract: the exit codes an operator's lock/cron wrapper
// is written against, the pinned slugification rule, and the two failure
// scopes of `ledger sync`.

// TestExitContract: 0 = report on stdout; 1 = error document on stderr;
// 2 = usage.
func TestExitContract(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")

	run := func(args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(bridgeBin, args...)
		cmd.Env = append(os.Environ(), fakegh.EnvState+"="+f.ghState, fakegh.EnvLogin+"=operator",
			fakegh.EnvFailAt+"=", fakegh.EnvFailAfter+"=")
		var so, se strings.Builder
		cmd.Stdout, cmd.Stderr = &so, &se
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return code, so.String(), se.String()
	}
	ok := []string{"sync", "--repo", f.repo, "--ledger", f.slug, "--store", f.dir,
		"--ledger-bin", ledgerBin, "--gh-bin", ghBin, "--done", f.done, "--not-planned", f.notPlanned}

	// 2 — usage.
	for _, args := range [][]string{
		{},
		{"frobnicate"},
		{"sync", "--repo", f.repo},   // no --ledger
		{"sync", "--ledger", f.slug}, // no --repo
		{"sync", "--repo", f.repo, "--ledger", f.slug, "--list-limit", "0"},
		append(append([]string{}, ok...), "stray-positional"),
	} {
		if code, _, _ := run(args...); code != exitUsage {
			t.Fatalf("args %v: want exit %d, got %d", args, exitUsage, code)
		}
	}

	// 0 — report on STDOUT, and it is the documented shape.
	code, stdout, stderr := run(ok...)
	if code != exitOK {
		t.Fatalf("want exit 0, got %d (stderr: %s)", code, stderr)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("the report must be JSON on stdout: %v\n%s", err, stdout)
	}
	for _, field := range []string{"ok", "repo", "ledger", "gh_mutations", "board_writes",
		"cursor", "divergences", "actions"} {
		if _, has := report[field]; !has {
			t.Fatalf("the report must carry %q: %s", field, stdout)
		}
	}
	// A converged run's actions list marshals as [], never JSON null: a
	// consumer must not have to special-case the fixed point.
	code, stdout, stderr = run(ok...)
	if code != exitOK {
		t.Fatalf("the second run: exit %d (%s)", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	if _, isList := report["actions"].([]any); !isList {
		t.Fatalf("actions must be a list even when empty, got %#v", report["actions"])
	}

	// 1 — error document on STDERR. A second repo is the refusal at hand.
	code, stdout, stderr = run(append(append([]string{}, ok[:1]...),
		"--repo", "prime-radiant-inc/elsewhere", "--ledger", f.slug, "--store", f.dir,
		"--ledger-bin", ledgerBin, "--gh-bin", ghBin, "--done", f.done, "--not-planned", f.notPlanned)...)
	if code != exitError {
		t.Fatalf("want exit 1, got %d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("an error run must write nothing to stdout: %q", stdout)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stderr), &doc); err != nil {
		t.Fatalf("the error must be a JSON document on stderr: %v\n%s", err, stderr)
	}
	if doc["ok"] != false || doc["error"] == nil {
		t.Fatalf("the error document must carry ok:false and error: %s", stderr)
	}
}

// TestSlugificationIsPinned: lowercase, non-grammar characters to `-`,
// collapsed, 48-char truncate; empty result becomes issue-<n>; a collision
// takes a -<n> suffix computed locally.
func TestSlugificationIsPinned(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"warm the cache on boot", "warm-the-cache-on-boot"},
		{"Fix The Retry Storm!", "fix-the-retry-storm"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"multiple   ---  separators", "multiple-separators"},
		{"CVE-2026-1234: heap overflow", "cve-2026-1234-heap-overflow"},
		{"日本語だけ", ""},
		{"!!!", ""},
		{strings.Repeat("a", 60), strings.Repeat("a", 48)},
		{strings.Repeat("ab-", 20), strings.TrimSuffix(strings.Repeat("ab-", 16), "-")},
	} {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// The empty and collision cases go through uniqueKey.
	s := &Syncer{keys: map[string]*KeyState{"taken": {}, "taken-7": {}}}
	if got := s.uniqueKey("", 42); got != "issue-42" {
		t.Errorf("an empty slug must become issue-<n>, got %q", got)
	}
	if got := s.uniqueKey("free", 7); got != "free" {
		t.Errorf("an uncontested slug must stand, got %q", got)
	}
	if got := s.uniqueKey("taken", 7); got != "taken-7-2" {
		t.Errorf("a doubly-taken slug must keep suffixing, got %q", got)
	}
	if got := s.uniqueKey("taken", 9); got != "taken-9" {
		t.Errorf("a collision takes the issue number as its suffix, got %q", got)
	}
	// The reserved state key counts as taken even though no board key exists.
	if got := s.uniqueKey(stateKey, 3); got == stateKey {
		t.Errorf("the reserved state key must never be minted: %q", got)
	}
}

// TestSyncFailureScoping: `ledger sync` takes no slug selector, so a fleet
// store holds slugs the bridge has nothing to do with. Abort iff OUR OWN
// slug failed; warn on every other slug's failure. A blanket abort couples
// the bridge's availability to every dead remote in the operator's store.
//
// Driven against a stand-in `ledger` so both branches are reachable
// deterministically — and so the exit-3 document is read off STDOUT, which is
// where sync and push write it.
func TestSyncFailureScoping(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "ledger-stub")
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	doc := func(outcomes string) string {
		return fmt.Sprintf(`#!/bin/sh
if [ "$1" = sync ]; then
  echo '{"ok":false,"error":"partial_failure","message":"some slugs did not sync","synced":[%s]}'
  exit 3
fi
exit 0
`, outcomes)
	}

	t.Run("a foreign slug's failure only warns", func(t *testing.T) {
		bin := write(t, doc(`{"slug":"issues","result":"synced"},{"slug":"someone-else","result":"failed","detail":"dead remote"}`))
		mine, others := Board{Bin: bin, Slug: "issues"}.Sync()
		if mine != nil {
			t.Fatalf("a foreign slug's failure must not abort: %v", mine)
		}
		if len(others) != 1 || others[0].Slug != "someone-else" {
			t.Fatalf("the foreign failure must be reported: %+v", others)
		}
	})

	t.Run("our own slug's failure aborts", func(t *testing.T) {
		bin := write(t, doc(`{"slug":"issues","result":"failed","detail":"diverged"},{"slug":"someone-else","result":"synced"}`))
		mine, others := Board{Bin: bin, Slug: "issues"}.Sync()
		if mine == nil {
			t.Fatal("our own slug's failure must abort")
		}
		if !strings.Contains(mine.Error(), "issues") {
			t.Fatalf("the abort must name the slug: %v", mine)
		}
		if len(others) != 0 {
			t.Fatalf("nothing else failed: %+v", others)
		}
	})

	t.Run("a total sync failure aborts", func(t *testing.T) {
		bin := write(t, "#!/bin/sh\nif [ \"$1\" = sync ]; then echo '{\"error\":\"git_failed\",\"message\":\"no\"}' 1>&2; exit 1; fi\nexit 0\n")
		mine, _ := Board{Bin: bin, Slug: "issues"}.Sync()
		if mine == nil {
			t.Fatal("a sync that could not run at all must abort — a stale replica mints duplicates")
		}
	})

	t.Run("a clean sync is silent", func(t *testing.T) {
		bin := write(t, "#!/bin/sh\necho '{\"ok\":true,\"synced\":[]}'\nexit 0\n")
		mine, others := Board{Bin: bin, Slug: "issues"}.Sync()
		if mine != nil || len(others) != 0 {
			t.Fatalf("a clean sync must report nothing: %v %+v", mine, others)
		}
	})
}

// TestSyncFailureAbortsBeforeTouchingGitHub: the abort must happen before
// any transport call, since acting on a replica that could not merge the
// others is exactly how duplicate issues get minted.
func TestSyncFailureAbortsBeforeTouchingGitHub(t *testing.T) {
	f := newIssueFixture(t)
	f.seed("cache-warm", "warm the cache on boot", "jesse")
	stub := filepath.Join(t.TempDir(), "ledger-stub")
	body := "#!/bin/sh\nif [ \"$1\" = sync ]; then echo '{\"ok\":false,\"error\":\"partial_failure\"," +
		"\"synced\":[{\"slug\":\"issues\",\"result\":\"failed\",\"detail\":\"diverged\"}]}'; exit 3; fi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	s := f.syncer()
	s.Board.Bin = stub
	if _, err := s.Run(); err == nil {
		t.Fatal("the run must abort")
	}
	if got := f.ghCalls(); got != 0 {
		t.Fatalf("the abort must precede every transport call, got %d calls", got)
	}
}

// TestStampedForeignIssueIsRefused is the repo-side half of
// one-repo-one-board, and the mitigation for a gap this build demonstrated
// LIVE: a second board bridged to an already-bound repo seeded 14 keys and
// rewrote the `ledger-key:` lines of issues the first board owned — which
// destroys the stamp's crash-recovery guarantee for every orphan still in
// the adoption window.
//
// A STAMPED issue whose hint names a key not on this board provably belongs
// to another board's bridge. It is warned and SKIPPED: never seeded, never
// body-rewritten, and its mirrored comments never re-imported. This is the
// hijack rule's conservatism applied to the stamp — a refusal, granting the
// body no new authority.
func TestStampedForeignIssueIsRefused(t *testing.T) {
	first := newIssueFixture(t)
	first.seed("cache-warm", "warm the cache on boot", "jesse")
	first.ledgerOK("note", "-k", kindHand, "--key", "cache-warm", "-m", "the first board said this", "--as", "jesse")
	first.converge("operator", 4)
	if countSubstr(first.commentBodies(1), "the first board said this") != 1 {
		t.Fatalf("setup: %v", first.commentBodies(1))
	}
	bodyBefore := first.ghLoad().Issue(1).Body

	// A FRESH board bridged to the same repo. Its chain resolves none of the
	// first board's event ids, so without the refusal every mirrored comment
	// would import as a human's.
	second := newIssueFixture(t)
	second.ghState, second.repo = first.ghState, first.repo
	r := second.syncOK("operator")

	if !hasWarning(r, "belongs to another board's bridge") {
		t.Fatalf("the stamped foreign issue must be refused by name: %s", mustJSON(t, r))
	}
	if got := len(second.keyList()); got != 0 {
		t.Fatalf("it must not be seeded, got keys %v", second.keyList())
	}
	if got := len(second.notes("comment")); got != 0 {
		t.Fatalf("its comments must not import: %+v", second.notes("comment"))
	}
	if got := second.ghLoad().Issue(1).Body; got != bodyBefore {
		t.Fatalf("the body must not be rewritten:\n%q\n%q", bodyBefore, got)
	}
	if r.GHMutations != 0 {
		t.Fatalf("a refused issue must cost zero GitHub writes: %s", mustJSON(t, r))
	}
	// And it converges: a run that refuses everything is a fixed point.
	second.converge("operator", 3)
}

// TestUnstampedForeignIssueStillBinds pins the BOUND the refusal leaves,
// honestly. The stamp is the only repo-side evidence there is; strip it (a
// human editing the body, an issue the bridge never created) and the binding
// doctrine is back to "whichever board runs first, permanently".
//
// If a board-discriminated marker (format v3) ever lands, this is the test
// that should go red and tell whoever shipped it what changed.
func TestUnstampedForeignIssueStillBinds(t *testing.T) {
	first := newIssueFixture(t)
	first.seed("cache-warm", "warm the cache on boot", "jesse")
	first.ledgerOK("note", "-k", kindHand, "--key", "cache-warm", "-m", "the first board said this", "--as", "jesse")
	first.converge("operator", 4)

	// A person edits the stamp out of the body.
	st := first.ghLoad()
	st.Issue(1).Body = strings.ReplaceAll(st.Issue(1).Body, bridgeStamp, "")
	first.ghSave(st)

	second := newIssueFixture(t)
	second.ghState, second.repo = first.ghState, first.repo
	r := second.syncOK("operator")

	texts := noteTexts(second.notes("comment"))
	if countSubstr(texts, "the first board said this") != 1 {
		t.Fatalf("DOCUMENTED BOUND CHANGED: with the stamp gone, a re-bridged repo still re-imports the "+
			"prior board's mirrored comments as human ones (the marker is not board-scoped). If format v3 "+
			"has landed, this test is the one to update. Got: %v\n%s", texts, mustJSON(t, r))
	}
}
