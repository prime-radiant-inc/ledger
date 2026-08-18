package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ledger/internal/gitx"
	"ledger/internal/store"
)

// TestPushBatchIsOneSubprocess is the batching claim itself (the delta from
// the spike's per-slug push, which is dozens of round trips at fleet
// scale): pushing three slugs must cost exactly ONE git subprocess call,
// not three. pushBatch is called directly against a call-counting Repo
// (the same technique scale_test.go uses for the whole-chain-cost claims)
// so the count isolates the push step itself from resolveRemote/
// repairRefspecs, which runPush runs first and which are already covered
// by Task 5's tests.
func TestPushBatchIsOneSubprocess(t *testing.T) {
	root := t.TempDir()
	remoteDir := root + "/remote.git"
	git(t, "", "init", "--bare", "-q", remoteDir)
	a := root + "/a"
	git(t, "", "clone", "-q", remoteDir, a)
	git(t, a, "config", "user.name", "t")
	git(t, a, "config", "user.email", "t@t")
	git(t, a, "commit", "-q", "--allow-empty", "-m", "init")

	seedBoard(t, a, "board1")
	seedBoard(t, a, "board2")
	seedBoard(t, a, "board3")

	var calls int64
	counted := store.Store{Repo: gitx.Repo{Dir: a, Calls: &calls}}
	c := &Ctx{Store: counted}

	outcomes := c.pushBatch("origin", []string{"board1", "board2", "board3"})
	if calls != 1 {
		t.Fatalf("a 3-slug batch push must be ONE subprocess, got %d", calls)
	}
	if len(outcomes) != 3 {
		t.Fatalf("expected 3 outcomes, got %+v", outcomes)
	}
	for _, o := range outcomes {
		if o.Result != "pushed" {
			t.Fatalf("expected every slug pushed, got %+v", o)
		}
	}
	for _, slug := range []string{"board1", "board2", "board3"} {
		local := git(t, a, "rev-parse", "refs/ledger/"+slug)
		remote := git(t, remoteDir, "rev-parse", "refs/ledger/"+slug)
		if local != remote {
			t.Fatalf("%s: remote ref did not land: local=%s remote=%s", slug, local, remote)
		}
	}
}

// TestPushDefaultPushesAllLocalSlugs: `ledger push` with no arguments
// publishes every local slug.
func TestPushDefaultPushesAllLocalSlugs(t *testing.T) {
	remote, a, _ := twoReplicas(t)
	seedBoard(t, a, "one")
	seedBoard(t, a, "two")

	got := syncResults(t, mustRun(t, a, "push"), "pushed")
	if got["one"]["result"] != "pushed" || got["two"]["result"] != "pushed" {
		t.Fatalf("push with no args must publish every local slug: %+v", got)
	}
	for _, slug := range []string{"one", "two"} {
		if git(t, remote, "rev-parse", "refs/ledger/"+slug) != git(t, a, "rev-parse", "refs/ledger/"+slug) {
			t.Fatalf("%s did not land on the remote", slug)
		}
	}
}

// TestPushSelectivePushesOnlyNamedSlug: naming one slug on the command line
// publishes only that slug — the privacy lever the spec names (one handoff
// ledger can go out without publishing everything else in the repo).
func TestPushSelectivePushesOnlyNamedSlug(t *testing.T) {
	remote, a, _ := twoReplicas(t)
	seedBoard(t, a, "public")
	seedBoard(t, a, "private")

	got := syncResults(t, mustRun(t, a, "push", "public"), "pushed")
	if len(got) != 1 || got["public"]["result"] != "pushed" {
		t.Fatalf("selective push must report only the named slug: %+v", got)
	}
	if git(t, remote, "rev-parse", "refs/ledger/public") != git(t, a, "rev-parse", "refs/ledger/public") {
		t.Fatal("the named slug did not land on the remote")
	}
	if out := git(t, remote, "for-each-ref", "--format=%(refname)", "refs/ledger/private"); out != "" {
		t.Fatalf("an unnamed slug must NOT be published: %q", out)
	}
}

// TestPushUnknownNamedSlugIsUnknownLedger: naming a slug that doesn't exist
// locally is a usage error, not a silently-empty push.
func TestPushUnknownNamedSlugIsUnknownLedger(t *testing.T) {
	dir := setup(t)
	git(t, dir, "remote", "add", "origin", "https://example.invalid/repo.git")
	_, se, code := run(t, dir, "push", "nope")
	if code != 4 || !strings.Contains(se, "unknown_ledger") {
		t.Fatalf("an unknown named slug must be unknown_ledger, exit 4: %d %q", code, se)
	}
}

// TestPushRejectedNonFastForwardIsPartialFailureNonForce: b advances the
// same slug both sides diverged on without syncing first, then pushes. The
// rejection must fold into the ok:false/error:"partial_failure" envelope
// (rev 6's pin, shared with sync), exit 3, the exact retry detail text, the
// remote ref must NOT move (push is never force), and git's own advice
// (suppressed via advice.pushNonFastForward=false) must never leak into the
// user's terminal.
func TestPushRejectedNonFastForwardIsPartialFailureNonForce(t *testing.T) {
	_, a, b := twoReplicas(t)
	seedBoard(t, a, "board")
	pushLedgerRef(t, a, "board")
	mustRun(t, b, "sync") // b adopts, so both sides share one root

	mustRun(t, a, "set", "task-a", "status=open", "--expect", "none", "-m", "from a", "--as", "alice")
	pushLedgerRef(t, a, "board") // a's remote ref moves ahead

	mustRun(t, b, "set", "task-b", "status=open", "--expect", "none", "-m", "from b", "--as", "bob")
	remoteBefore := currentRemoteHead(t, a, "board")

	so, se, code := run(t, b, "push")
	if code != 3 {
		t.Fatalf("a rejected push is a partial failure (exit 3), got %d\n%s", code, so)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(so), &doc); err != nil {
		t.Fatalf("payload not JSON: %v\n%s", err, so)
	}
	if doc["ok"] != false || doc["error"] != "partial_failure" {
		t.Fatalf("a rejected push must report ok:false/error:partial_failure: %v", doc)
	}
	got := syncResults(t, so, "pushed")
	if got["board"]["result"] != "rejected" {
		t.Fatalf("a diverged push must be rejected, not silently dropped: %+v", got)
	}
	detail, _ := got["board"]["detail"].(string)
	if detail != "run `ledger sync`, then retry `ledger push`" {
		t.Fatalf("rejection detail must be the exact retry instruction: %q", detail)
	}
	if strings.Contains(se, "git pull") || strings.Contains(se, "hint:") {
		t.Fatalf("git's own advice must never leak into stderr: %q", se)
	}
	if currentRemoteHead(t, a, "board") != remoteBefore {
		t.Fatal("a rejected push must never move the remote ref (push is non-force)")
	}
}

// TestPushRejectedRootMismatchNamesBothCreators: two clones independently
// CREATE the same slug (never synced, no shared root). b's push is
// rejected same as any non-fast-forward, but push's post-rejection fetch
// must diagnose the deeper cause and name both creators — never the plain
// retry instruction, which would send the operator toward a merge that can
// never succeed.
func TestPushRejectedRootMismatchNamesBothCreators(t *testing.T) {
	_, a, b := twoReplicas(t)
	seedBoard(t, a, "board") // created by alice inside seedBoard
	pushLedgerRef(t, a, "board")

	mustRun(t, b, "create", "board", "--scope", "b's own board", "--as", "bob")

	so, _, code := run(t, b, "push")
	if code != 3 {
		t.Fatalf("a root-mismatched push is a partial failure (exit 3), got %d\n%s", code, so)
	}
	got := syncResults(t, so, "pushed")
	if got["board"]["result"] != "rejected" {
		t.Fatalf("a root mismatch push must be rejected: %+v", got)
	}
	detail, _ := got["board"]["detail"].(string)
	for _, want := range []string{"alice", "bob", "ledger export", "ledger import"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("a root-mismatched rejection must name both creators and the export/import exit; missing %q in %q", want, detail)
		}
	}
}

// TestPushDegradedNoRemoteIsCleanNoOp: a repo with no remote is a
// legitimate way to run push, same as sync.
func TestPushDegradedNoRemoteIsCleanNoOp(t *testing.T) {
	dir := setup(t)
	so, _, code := run(t, dir, "push")
	if code != 0 {
		t.Fatalf("push with no remote must exit 0, got %d\n%s", code, so)
	}
	if !strings.Contains(so, "no git remote configured") {
		t.Fatalf("push must announce the degraded mode: %s", so)
	}
}

// TestPushAmbiguousRemoteReusesTask5Path: two remotes and nothing selects
// one is ambiguous_remote, exit 4 — the same resolveRemote path sync uses,
// wired through push too.
func TestPushAmbiguousRemoteReusesTask5Path(t *testing.T) {
	dir := setup(t)
	git(t, dir, "remote", "add", "one", "https://example.invalid/one.git")
	git(t, dir, "remote", "add", "two", "https://example.invalid/two.git")
	so, se, code := run(t, dir, "push")
	if code != 4 || !strings.Contains(se, "ambiguous_remote") {
		t.Fatalf("two remotes with no origin must be ambiguous_remote (exit 4), got %d\n%s\n%s", code, so, se)
	}
}

// currentRemoteHead reads a slug's ref straight off the bare remote by
// fetching it into a throwaway ref in dir, without touching dir's own
// refs/ledger namespace.
func currentRemoteHead(t *testing.T, dir, slug string) string {
	t.Helper()
	git(t, dir, "fetch", "-q", "origin", "refs/ledger/"+slug+":refs/push-test-peek")
	sha := git(t, dir, "rev-parse", "refs/push-test-peek")
	git(t, dir, "update-ref", "-d", "refs/push-test-peek")
	return sha
}
