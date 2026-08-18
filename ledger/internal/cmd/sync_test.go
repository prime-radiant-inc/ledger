package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// twoReplicas builds the trial's own topology in miniature: a bare remote
// and two clones, both `ledger init`ed. Everything below drives the real
// verbs against real git — no fakes, since the whole point is whether git
// does what the design says it does. push.go doesn't exist yet (Task 6), so
// tests that need to publish a local ledger to the bare remote use raw git
// (pushLedgerRef) rather than a `ledger push` that isn't built yet.
func twoReplicas(t *testing.T) (remote, a, b string) {
	t.Helper()
	root := t.TempDir()
	remote = root + "/remote.git"
	git(t, "", "init", "--bare", "-q", remote)
	for _, name := range []string{"a", "b"} {
		dir := root + "/" + name
		git(t, "", "clone", "-q", remote, dir)
		git(t, dir, "config", "user.name", "t")
		git(t, dir, "config", "user.email", "t@t")
	}
	a, b = root+"/a", root+"/b"
	git(t, a, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, a, "push", "-q", "origin", "HEAD:refs/heads/main")
	git(t, b, "fetch", "-q", "origin")
	mustRun(t, a, "init")
	mustRun(t, b, "init")
	return remote, a, b
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// mustRun runs a verb and fails the test on a non-zero exit.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	so, se, code := run(t, dir, args...)
	if code != 0 {
		t.Fatalf("ledger %s in %s: exit %d\n%s\n%s", strings.Join(args, " "), dir, code, so, se)
	}
	return so
}

// pushLedgerRef publishes one slug's ref to the bare remote with plain git —
// standing in for `ledger push` (Task 6, not yet built) so these tests can
// exercise sync's read side against real published state.
func pushLedgerRef(t *testing.T, dir, slug string) {
	t.Helper()
	git(t, dir, "push", "-q", "origin", "refs/ledger/"+slug+":refs/ledger/"+slug)
}

// syncResults maps slug -> result from a sync payload.
func syncResults(t *testing.T, payload, field string) map[string]map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		t.Fatalf("payload not JSON: %v\n%s", err, payload)
	}
	out := map[string]map[string]any{}
	list, _ := doc[field].([]any)
	for _, e := range list {
		m := e.(map[string]any)
		out[m["slug"].(string)] = m
	}
	return out
}

func seedBoard(t *testing.T, dir, slug string) {
	t.Helper()
	seedBoardAs(t, dir, slug, "alice")
}

// seedBoardAs is seedBoard with an explicit creator — the root-mismatch
// freshness fixture needs two independently-created same-slug boards whose
// creators differ but whose declared shape is otherwise identical, so
// `ready` still works on either side.
func seedBoardAs(t *testing.T, dir, slug, as string) {
	t.Helper()
	mustRun(t, dir, "create", slug, "--scope", "sync-test",
		"--field", "status=open,in-progress,done,failed", "--terminal", "status=done,failed",
		"--guard", "status", "--multi-field", "labels", "--stale-after", "2h", "--as", as)
}

// rawFetchTracking populates dir's tracking refs directly via git fetch,
// bypassing `ledger sync`'s merge step — the freshness tests' "fetched but
// not yet merged" fixture: the tracking ref moves while local stays put,
// exactly the state a read-time warning has to catch.
func rawFetchTracking(t *testing.T, dir, remote string) {
	t.Helper()
	git(t, dir, "fetch", "-q", "--prune", remote, "+refs/ledger/*:refs/ledger-remote/"+remote+"/*")
}

func statusID(t *testing.T, dir, slug, key string) string {
	t.Helper()
	var doc map[string]any
	json.Unmarshal([]byte(mustRun(t, dir, "status", key, "--ledger", slug)), &doc)
	vals := doc["values"].(map[string]any)
	return vals["status"].(map[string]any)["id"].(string)
}

func mergeCount(t *testing.T, dir, slug string) int {
	t.Helper()
	n, _ := strconv.Atoi(git(t, dir, "rev-list", "--merges", "--count", "refs/ledger/"+slug))
	return n
}

// TestSyncFourPerSlugCases walks the parent spec's whole per-slug rule in
// one scenario, in the order the cases arise in real use. The spec's own
// wording for the idle case is "no-op" (not the spike's "up-to-date" —
// applied here as a deliberate correction).
func TestSyncFourPerSlugCases(t *testing.T) {
	_, a, b := twoReplicas(t)
	seedBoard(t, a, "board")
	mustRun(t, a, "set", "task-1", "status=open", "--expect", "none", "-m", "first", "--as", "alice")
	pushLedgerRef(t, a, "board")

	// CASE 1 — no local ref: adoption at the tracking head, with the
	// provenance line the spec requires.
	got := syncResults(t, mustRun(t, b, "sync"), "synced")
	if got["board"]["result"] != "adopted" {
		t.Fatalf("a remote-only slug must be adopted: %+v", got)
	}
	if d, _ := got["board"]["detail"].(string); !strings.Contains(d, "created by alice") {
		t.Fatalf("adoption must announce the creator: %q", d)
	}
	if git(t, a, "rev-parse", "refs/ledger/board") != git(t, b, "rev-parse", "refs/ledger/board") {
		t.Fatal("adoption must land the tracking head verbatim")
	}

	// CASE 2 — tracking already contained in local: a no-op. Without this
	// rule chains grow one merge per sync forever, so assert both the
	// reported result and that nothing moved.
	before := git(t, b, "rev-parse", "refs/ledger/board")
	for i := 0; i < 5; i++ {
		got = syncResults(t, mustRun(t, b, "sync"), "synced")
		if got["board"]["result"] != "no-op" {
			t.Fatalf("idle sync %d must be a no-op: %+v", i, got)
		}
	}
	if git(t, b, "rev-parse", "refs/ledger/board") != before || mergeCount(t, b, "board") != 0 {
		t.Fatal("idle syncs must not grow the chain")
	}

	// CASE 3 — local behind tracking: fast-forward, no merge commit.
	mustRun(t, a, "set", "task-2", "status=open", "--expect", "none", "-m", "second", "--as", "alice")
	pushLedgerRef(t, a, "board")
	got = syncResults(t, mustRun(t, b, "sync"), "synced")
	if got["board"]["result"] != "fast-forward" {
		t.Fatalf("a behind local must fast-forward, not merge: %+v", got)
	}
	if mergeCount(t, b, "board") != 0 {
		t.Fatal("a fast-forward must not mint a merge commit")
	}

	// CASE 4 — true divergence: exactly ONE sentinel merge.
	mustRun(t, a, "set", "task-1", "status=in-progress", "--expect", statusID(t, a, "board", "task-1"),
		"-m", "alice claims", "--as", "alice")
	mustRun(t, b, "set", "task-2", "status=in-progress", "--expect", statusID(t, b, "board", "task-2"),
		"-m", "bob claims", "--as", "bob")
	pushLedgerRef(t, a, "board")
	got = syncResults(t, mustRun(t, b, "sync"), "synced")
	if got["board"]["result"] != "merged" {
		t.Fatalf("true divergence must produce a sentinel merge: %+v", got)
	}
	if n := mergeCount(t, b, "board"); n != 1 {
		t.Fatalf("divergence must produce EXACTLY one sentinel merge, got %d", n)
	}

	// Both sides' writes survive, and the sentinel is invisible to reads.
	spine := mustRun(t, b, "status", "--ledger", "board")
	if !strings.Contains(spine, "alice claims") || !strings.Contains(spine, "bob claims") {
		t.Fatalf("merge lost a write:\n%s", spine)
	}
	raw := mustRun(t, b, "tail", "--raw", "-n", "50", "--ledger", "board")
	if strings.Contains(raw, `"sync"`) {
		t.Fatalf("a sentinel must never surface in a read:\n%s", raw)
	}

	// And the replicas converge: same refs, byte-identical projections.
	pushLedgerRef(t, b, "board")
	mustRun(t, a, "sync")
	if git(t, a, "rev-parse", "refs/ledger/board") != git(t, b, "rev-parse", "refs/ledger/board") {
		t.Fatal("replicas must converge on the same ref after sync")
	}
	for _, verb := range []string{"status", "tail", "show", "notes"} {
		sa := mustRun(t, a, verb, "--ledger", "board")
		sb := mustRun(t, b, verb, "--ledger", "board")
		if sa != sb {
			t.Fatalf("%s diverges across replicas:\nA: %s\nB: %s", verb, sa, sb)
		}
	}
}

// TestSyncRefusesDifferentRoots: two clones that independently created one
// slug have nothing to merge. Sync must refuse, name BOTH creators, point at
// export/import — never splice two unrelated ledgers together — and the
// whole invocation must fold into the ok:false/error:"partial_failure"
// envelope (rev 7 pin), never a second error document.
func TestSyncRefusesDifferentRoots(t *testing.T) {
	_, a, b := twoReplicas(t)
	seedBoard(t, a, "board")
	pushLedgerRef(t, a, "board")

	// b creates its own "board" from scratch, never having synced.
	mustRun(t, b, "create", "board", "--scope", "b's own board", "--as", "bob")

	so, se, code := run(t, b, "sync")
	if code != 3 {
		t.Fatalf("a root mismatch is a partial failure (exit 3), got %d\n%s", code, so)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(so), &doc); err != nil {
		t.Fatalf("payload not JSON: %v\n%s", err, so)
	}
	if doc["ok"] != false {
		t.Fatalf("a partial failure must report ok:false: %v", doc)
	}
	if doc["error"] != "partial_failure" {
		t.Fatalf("a partial failure must report error:\"partial_failure\": %v", doc)
	}
	if hint, _ := doc["hint"].(string); !strings.Contains(hint, "synced") {
		t.Fatalf("the hint must point at the outcomes array: %v", doc["hint"])
	}
	if strings.Contains(se, "error") {
		t.Fatalf("the envelope's own document is the whole answer; no second error write to stderr: %q", se)
	}
	got := syncResults(t, so, "synced")
	if got["board"]["result"] != "refused" {
		t.Fatalf("different roots must be refused, not merged: %+v", got)
	}
	detail, _ := got["board"]["detail"].(string)
	for _, want := range []string{"alice", "bob", "ledger export", "ledger import"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("refusal must name both creators and the export/import exit; missing %q in %q", want, detail)
		}
	}
	if mergeCount(t, b, "board") != 0 {
		t.Fatal("a refused sync must not touch the local ref")
	}
}

// TestSyncRefusesGraftedMultiRootChain: a candidate chain with more than one
// root (a hand-crafted graft — never anything the tool itself could
// produce) must be refused before the local ref is ever moved or created,
// naming both roots and the tracking ref, with a REMOTE-side repair hint
// (never export/import — that exit re-slugs the LOCAL chain and would just
// invite adoption of the poisoned one). An un-grafted slug in the same sync
// must still succeed.
func TestSyncRefusesGraftedMultiRootChain(t *testing.T) {
	_, a, b := twoReplicas(t)

	// The ordinary, un-grafted slug that must still sync fine alongside the
	// refusal.
	seedBoard(t, a, "board")
	pushLedgerRef(t, a, "board")

	// root1: a legitimate "graft" board created by alice in `a`.
	mustRun(t, a, "create", "graft", "--scope", "legit", "--as", "alice")
	root1 := git(t, a, "rev-list", "--max-parents=0", "refs/ledger/graft")

	// root2: an unrelated foreign board created by mallory in a throwaway
	// clone, fetched into a's odb.
	foreign := t.TempDir()
	git(t, "", "init", "-q", "-b", "main", foreign)
	git(t, foreign, "config", "user.name", "t")
	git(t, foreign, "config", "user.email", "t@t")
	mustRun(t, foreign, "init")
	mustRun(t, foreign, "create", "graft", "--scope", "foreign", "--as", "mallory")
	root2 := git(t, foreign, "rev-parse", "refs/ledger/graft")
	git(t, a, "fetch", "-q", foreign, "refs/ledger/graft:refs/graft-import")

	// Splice a merge commit with both roots as parents directly onto
	// refs/ledger/graft — a hand-crafted graft, never anything `ledger sync`
	// or `ledger create` could produce on its own.
	tree := git(t, a, "rev-parse", "refs/ledger/graft^{tree}")
	grafted := git(t, a, "commit-tree", tree, "-p", root1, "-p", root2, "-m", "graft")
	git(t, a, "update-ref", "refs/ledger/graft", grafted)
	pushLedgerRef(t, a, "graft")

	so, _, code := run(t, b, "sync")
	if code != 3 {
		t.Fatalf("a multi-root chain is a partial failure (exit 3), got %d\n%s", code, so)
	}
	got := syncResults(t, so, "synced")
	if got["graft"]["result"] != "refused" {
		t.Fatalf("a grafted multi-root chain must be refused, not adopted: %+v", got)
	}
	detail, _ := got["graft"]["detail"].(string)
	for _, want := range []string{root1[:10], root2[:10], "refs/ledger-remote/origin/graft", "alice", "mallory"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("refusal must name both roots, their creators, and the tracking ref; missing %q in %q", want, detail)
		}
	}
	if strings.Contains(detail, "ledger export") || strings.Contains(detail, "ledger import") {
		t.Fatalf("multi-root refusal's hint is remote-side repair, NOT export/import: %q", detail)
	}
	if _, _, code := run(t, b, "show", "--ledger", "graft"); code == 0 {
		t.Fatal("a refused multi-root sync must not have created a local ref")
	}

	// The un-grafted slug in the same sync invocation must still succeed.
	if got["board"]["result"] != "adopted" {
		t.Fatalf("an un-grafted slug in the same sync must still sync: %+v", got)
	}
}

// TestAdoptionRevalidatesDeclarations: adoption is a third meta-minting
// path, so a board arriving by sync with a broken ready-capable shape is
// refused with the defect named, never minted.
func TestAdoptionRevalidatesDeclarations(t *testing.T) {
	_, a, b := twoReplicas(t)
	// Build a board that is ready-capable-shaped but missing --guard status.
	// create itself refuses this, so the broken chain is planted directly —
	// which is exactly the threat model: a chain minted by something other
	// than this tool's create.
	seedBoard(t, a, "broken")
	pushLedgerRef(t, a, "broken")

	// Rewrite the pushed chain's meta.json to drop the guard, then move the
	// remote ref to the doctored root.
	root := git(t, a, "rev-list", "--max-parents=0", "refs/ledger/broken")
	metaJSON := git(t, a, "cat-file", "-p", root+":meta.json")
	var meta map[string]any
	json.Unmarshal([]byte(metaJSON), &meta)
	delete(meta, "guard")
	doctored, _ := json.Marshal(meta)
	blob := gitStdin(t, a, string(doctored), "hash-object", "-w", "--stdin")
	evBlob := git(t, a, "rev-parse", root+":event.json")
	tree := gitStdin(t, a, "100644 blob "+evBlob+"\tevent.json\n100644 blob "+blob+"\tmeta.json\n", "mktree")
	commit := gitStdin(t, a, "", "commit-tree", tree, "-m", "doctored")
	git(t, a, "push", "-q", "--force", "origin", commit+":refs/ledger/broken")

	so, _, code := run(t, b, "sync")
	if code != 3 {
		t.Fatalf("an invalid arriving board is a partial failure (exit 3), got %d\n%s", code, so)
	}
	got := syncResults(t, so, "synced")
	if got["broken"]["result"] != "refused" {
		t.Fatalf("a broken board must be refused, not minted: %+v", got)
	}
	if d, _ := got["broken"]["detail"].(string); !strings.Contains(d, "guard status") {
		t.Fatalf("the refusal must name the declaration defect: %q", d)
	}
	if _, _, code := run(t, b, "show", "--ledger", "broken"); code == 0 {
		t.Fatal("a refused adoption must not have created a local ref")
	}
}

func gitStdin(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Stdin = strings.NewReader(stdin)
	out, err := c.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// TestSyncAdoptionRefusesBadSlug (review finding 3a): adoption is a third
// slug-creation path alongside create/import, so a remote publishing
// refs/ledger/<bad-slug> directly (never through this tool, which enforces
// the grammar itself — the threat model is a foreign or corrupted
// publisher) must be refused with the same bad_slug shape, never silently
// minted as an ungoverned local ref.
func TestSyncAdoptionRefusesBadSlug(t *testing.T) {
	_, a, b := twoReplicas(t)
	// Plant a grammar-invalid slug ref directly on the remote via raw git —
	// uppercase and underscore both fail [a-z0-9][a-z0-9-]*.
	sha := git(t, a, "commit-tree", git(t, a, "rev-parse", "HEAD^{tree}"), "-m", "planted")
	git(t, a, "push", "-q", "origin", sha+":refs/ledger/Bad_Slug")

	so, _, code := run(t, b, "sync")
	if code != 3 {
		t.Fatalf("a bad-slug adoption is a partial failure (exit 3), got %d\n%s", code, so)
	}
	got := syncResults(t, so, "synced")
	if got["Bad_Slug"]["result"] != "refused" {
		t.Fatalf("a grammar-invalid slug must be refused, not adopted: %+v", got)
	}
	detail, _ := got["Bad_Slug"]["detail"].(string)
	if !strings.Contains(detail, "bad_slug") {
		t.Fatalf("the refusal must name bad_slug: %q", detail)
	}
	if _, _, code := run(t, b, "show", "--ledger", "Bad_Slug"); code == 0 {
		t.Fatal("a refused bad-slug adoption must not have created a local ref")
	}
}

// TestSyncAdoptionCASFailureReportsRealCauseNotRace (review finding 3b): a
// non-race CAS failure — here a D/F ref-name conflict, one of several named
// classes (illegal ref name, macOS case-aliasing, lock contention are the
// others) — must be diagnosed truthfully, never misreported as "appeared
// mid-adoption". The slug "a" itself is perfectly valid; the conflict comes
// from a stray sibling ref already occupying the git ref-storage path
// adoption needs, unrelated to slug grammar (finding 3a's own test already
// covers the grammar gate).
func TestSyncAdoptionCASFailureReportsRealCauseNotRace(t *testing.T) {
	_, a, b := twoReplicas(t)
	seedBoard(t, a, "a")
	pushLedgerRef(t, a, "a")

	// A stray ref at refs/ledger/a/stray makes refs/ledger/a un-creatable —
	// git's loose-ref storage can't have both a leaf and a directory at the
	// same path. Probed directly: git's own message is "cannot lock ref
	// 'refs/ledger/a': 'refs/ledger/a/stray' exists; cannot create
	// 'refs/ledger/a'".
	git(t, b, "update-ref", "refs/ledger/a/stray", git(t, b, "rev-parse", "origin/main"))

	so, _, code := run(t, b, "sync")
	if code != 3 {
		t.Fatalf("a blocked adoption is a partial failure (exit 3), got %d\n%s", code, so)
	}
	got := syncResults(t, so, "synced")
	if got["a"]["result"] == "adopted" {
		t.Fatalf("a D/F-conflicted adoption must not silently succeed: %+v", got)
	}
	detail, _ := got["a"]["detail"].(string)
	if strings.Contains(detail, "appeared mid-adoption") {
		t.Fatalf("a D/F ref conflict must not be misreported as a race: %q", detail)
	}
	if !strings.Contains(detail, "refs/ledger/a/stray") || !strings.Contains(detail, "exists") {
		t.Fatalf("the refusal must surface git's real diagnosis: %q", detail)
	}
}

// TestSyncRaceFastForwardRetriesInsteadOfFailing (review finding 2): two
// REAL processes both fast-forward the same local ref to the same tracking
// head at once. Under the pre-fix code the ff case had a single CAS shot,
// so the loser reported exit-3 partial_failure for what is the design's
// normal concurrent state (a local agent's own sync racing another). Fixed
// code loops back, re-reads the fresh head, and finds a no-op — never a
// merge, never a failure.
func TestSyncRaceFastForwardRetriesInsteadOfFailing(t *testing.T) {
	_, a, b := twoReplicas(t)
	seedBoard(t, a, "board")
	pushLedgerRef(t, a, "board")
	if _, se, code := execLedger(t, b, "sync"); code != 0 {
		t.Fatalf("adopt: %s", se)
	}

	for i := 0; i < 10; i++ {
		task := fmt.Sprintf("ff-race-task-%d", i)
		if _, se, code := execLedger(t, a, "set", task, "status=open", "--expect", "none", "-m", "from a", "--as", "alice"); code != 0 {
			t.Fatalf("round %d: a advance: %s", i, se)
		}
		pushLedgerRef(t, a, "board")
		// Pre-fetch the tracking ref via raw git (not `ledger sync`, which
		// would also move b's local ref before the race starts). This keeps
		// each raced invocation's OWN internal fetch a no-op — nothing new
		// to bring in — so the two processes contend on the LOCAL ref's CAS
		// alone, the thing finding 2 is actually about, rather than also
		// racing git fetch's own ref-transaction on the tracking ref itself
		// (a real but different contention point: two concurrent `git
		// fetch`es into the same tracking ref can lock-fail each other).
		git(t, b, "fetch", "-q", "--prune", "origin", "+refs/ledger/*:refs/ledger-remote/origin/*")
		r1, r2 := raceLedger(t, b, []string{"sync"}, []string{"sync"})
		for j, r := range []raceResult{r1, r2} {
			if r.code != 0 {
				t.Fatalf("round %d side %d: a raced fast-forward must retry, not fail: exit %d\nstdout: %s\nstderr: %s",
					i, j, r.code, r.stdout, r.stderr)
			}
		}
		if n := mergeCount(t, b, "board"); n != 0 {
			t.Fatalf("round %d: a pure fast-forward race must never mint a merge, got %d merges", i, n)
		}
	}
}

// TestSyncRaceRetryReclassifiesInsteadOfDoublingTheMerge (review finding 1):
// two REAL processes both sync the same true divergence at once. Whichever
// loses the CAS race must re-read the fresh head and re-decide
// no-op/fast-forward/merge against it — not blindly rebuild a merge from
// stale parents, which either mints a second, pointless sentinel, or (if
// the winner already landed exactly `track`) gets silently deduped into a
// single-parent commit for nothing. Across 10 rounds, both processes must
// succeed and exactly one NEW sentinel merge must land per round.
func TestSyncRaceRetryReclassifiesInsteadOfDoublingTheMerge(t *testing.T) {
	_, a, b := twoReplicas(t)
	seedBoard(t, a, "board")
	pushLedgerRef(t, a, "board")
	if _, se, code := execLedger(t, b, "sync"); code != 0 {
		t.Fatalf("adopt: %s", se)
	}

	for i := 0; i < 10; i++ {
		// a advances and publishes.
		task := fmt.Sprintf("race-task-%d", i)
		if _, se, code := execLedger(t, a, "set", task, "status=open", "--expect", "none", "-m", "from a", "--as", "alice"); code != 0 {
			t.Fatalf("round %d: a advance: %s", i, se)
		}
		pushLedgerRef(t, a, "board")
		// Fetch b's tracking ref directly — NOT via `ledger sync`, which
		// would also fast-forward b's own local ref and leave nothing
		// diverged for this round to race over.
		git(t, b, "fetch", "-q", "--prune", "origin", "+refs/ledger/*:refs/ledger-remote/origin/*")

		// b's own local diverges too.
		btask := fmt.Sprintf("race-btask-%d", i)
		if _, se, code := execLedger(t, b, "set", btask, "status=open", "--expect", "none", "-m", "from b", "--as", "bob"); code != 0 {
			t.Fatalf("round %d: b advance: %s", i, se)
		}

		before := mergeCount(t, b, "board")
		r1, r2 := raceLedger(t, b, []string{"sync"}, []string{"sync"})
		for j, r := range []raceResult{r1, r2} {
			if r.code != 0 {
				t.Fatalf("round %d side %d: concurrent sync must not fail (must re-classify, not error): exit %d\nstdout: %s\nstderr: %s",
					i, j, r.code, r.stdout, r.stderr)
			}
		}
		if n := mergeCount(t, b, "board") - before; n != 1 {
			t.Fatalf("round %d: a raced pair of syncs must mint exactly ONE new sentinel merge, got %d", i, n)
		}
	}
}

// TestDegradedNoRemoteIsACleanNoOp: a repo with no remote is a legitimate
// way to run. Sync must say so and exit 0, never error.
func TestDegradedNoRemoteIsACleanNoOp(t *testing.T) {
	dir := setup(t) // a plain repo with a ledger and no remotes
	so, se, code := run(t, dir, "sync")
	if code != 0 {
		t.Fatalf("sync with no remote must exit 0, got %d\n%s\n%s", code, so, se)
	}
	if !strings.Contains(so, "no git remote configured") {
		t.Fatalf("sync must announce the degraded mode: %s", so)
	}
}

// TestSyncWithRemoteButNoTrackedSlugsEmitsEmptyArray: a configured, reachable
// remote that simply has no ledger refs yet must report an empty outcomes
// array (`[]`), never JSON `null` — a consumer ranging over "synced" must
// not have to special-case the zero-slugs shape.
func TestSyncWithRemoteButNoTrackedSlugsEmitsEmptyArray(t *testing.T) {
	_, a, _ := twoReplicas(t) // a bare remote with no ledgers pushed at all
	so, _, code := run(t, a, "sync")
	if code != 0 {
		t.Fatalf("a reachable remote with nothing tracked must exit 0, got %d\n%s", code, so)
	}
	if !strings.Contains(so, `"synced": []`) {
		t.Fatalf("empty outcomes must marshal as [], not null: %s", so)
	}
	if !strings.Contains(so, "nothing to sync") {
		t.Fatalf("expected a nothing-to-sync note: %s", so)
	}
}

// TestSyncAmbiguousRemote: two remotes and nothing selects one is
// ambiguous_remote (exit 4), distinct from bad_value's "you named a remote
// that doesn't exist" — never a silent no-op and never a guess.
func TestSyncAmbiguousRemote(t *testing.T) {
	dir := setup(t)
	git(t, dir, "remote", "add", "one", "https://example.invalid/one.git")
	git(t, dir, "remote", "add", "two", "https://example.invalid/two.git")
	so, se, code := run(t, dir, "sync")
	if code != 4 {
		t.Fatalf("two remotes with no origin must be ambiguous_remote (exit 4), got %d\n%s\n%s", code, so, se)
	}
	if !strings.Contains(se, "ambiguous_remote") {
		t.Fatalf("expected ambiguous_remote: %q", se)
	}
	if !strings.Contains(se, "--remote") || !strings.Contains(se, "one") || !strings.Contains(se, "two") {
		t.Fatalf("hint must list both candidates and the --remote fix: %q", se)
	}
}

// TestSyncBadRemoteFlagStaysBadValue: --remote naming a remote that doesn't
// exist is bad_value, not ambiguous_remote — the two must stay
// distinguishable so an agent can tell "you named nothing and there are
// several" from "you named something wrong".
func TestSyncBadRemoteFlagStaysBadValue(t *testing.T) {
	dir := setup(t)
	_, se, code := run(t, dir, "sync", "--remote", "nope")
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("--remote naming an unknown remote must be bad_value: %d %q", code, se)
	}
}

// TestRefspecRepairSurvivesRemoteRename is round 5's verified failure: `git
// remote rename` leaves the old refspec behind, which repopulates the dead
// tracking namespace after every prune — a permanent oscillation. One sync
// must rewrite it, and three cycles must not resurrect it.
func TestRefspecRepairSurvivesRemoteRename(t *testing.T) {
	_, a, _ := twoReplicas(t)
	seedBoard(t, a, "board")
	pushLedgerRef(t, a, "board")
	mustRun(t, a, "sync")
	if git(t, a, "for-each-ref", "--format=%(refname)", "refs/ledger-remote/origin/") == "" {
		t.Fatal("sync must populate the tracking namespace")
	}

	git(t, a, "remote", "rename", "origin", "upstream")
	for i := 0; i < 3; i++ {
		mustRun(t, a, "sync", "--remote", "upstream")
		if got := git(t, a, "for-each-ref", "--format=%(refname)", "refs/ledger-remote/origin/"); got != "" {
			t.Fatalf("cycle %d: the dead namespace was repopulated: %s", i, got)
		}
		fetches := git(t, a, "config", "--get-all", "remote.upstream.fetch")
		if strings.Contains(fetches, "refs/ledger-remote/origin/") {
			t.Fatalf("cycle %d: the stale refspec survived repair: %s", i, fetches)
		}
		if !strings.Contains(fetches, "refs/ledger-remote/upstream/") {
			t.Fatalf("cycle %d: the correct refspec was not installed: %s", i, fetches)
		}
	}
}

// TestInitInstallsRefspecForSoleRemote: `ledger init` in a freshly cloned
// repo (which already has "origin") installs the ledger refspec immediately
// — a bare `git fetch` right after init is sync-ready without waiting for
// the first `ledger sync` to repair it.
func TestInitInstallsRefspecForSoleRemote(t *testing.T) {
	root := t.TempDir()
	remote := root + "/remote.git"
	git(t, "", "init", "--bare", "-q", remote)
	dir := root + "/clone"
	git(t, "", "clone", "-q", remote, dir)
	git(t, dir, "config", "user.name", "t")
	git(t, dir, "config", "user.email", "t@t")

	so, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal(so)
	}
	fetches := git(t, dir, "config", "--get-all", "remote.origin.fetch")
	if !strings.Contains(fetches, "refs/ledger-remote/origin/") {
		t.Fatalf("init must install the ledger refspec for the sole remote: %q", fetches)
	}
}

// TestInitSkipsRefspecInstallWithNoRemote: a plain, remote-less repo must
// init cleanly with no refspec side effect — nothing to resolve, nothing to
// error about (init has no --remote flag to disambiguate with anyway).
func TestInitSkipsRefspecInstallWithNoRemote(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "init")
	if code != 0 {
		t.Fatal(so)
	}
	var doc map[string]any
	json.Unmarshal([]byte(so), &doc)
	if _, present := doc["refspec_repairs"]; present {
		t.Fatalf("no remote configured: no refspec repair should be reported: %v", doc)
	}
}

// ---- freshness (sync design Addition 3's freshness bullet) ----

// freshnessFixture builds the read-time freshness tests' shared setup: a
// ready-capable "board", replica b fully synced (adopted) to a's first
// write, then replica a advances by exactly one more real event and pushes
// it. b's tracking ref is NOT fetched by this fixture — a caller wanting the
// "fetched but unmerged" state calls rawFetchTracking itself; a caller
// wanting the "never fetched again" state uses b as-is.
func freshnessFixture(t *testing.T) (a, b string) {
	t.Helper()
	_, a, b = twoReplicas(t)
	seedBoard(t, a, "board")
	mustRun(t, a, "set", "task-1", "status=open", "--expect", "none", "-m", "first", "--as", "alice")
	pushLedgerRef(t, a, "board")
	if got := syncResults(t, mustRun(t, b, "sync"), "synced"); got["board"]["result"] != "adopted" {
		t.Fatalf("setup: expected adoption, got %+v", got)
	}
	prev := statusID(t, a, "board", "task-1")
	mustRun(t, a, "set", "task-1", "status=in-progress", "--expect", prev, "-m", "second", "--as", "alice")
	pushLedgerRef(t, a, "board")
	return a, b
}

// TestFreshnessWarnsFetchedButUnmerged: a replica that has fetched but not
// yet merged a remote's new events warns on every read verb the spec names
// (ready, show, status — both the keyless spine and a single key's
// drill-down), in JSON as a top-level `freshness` sibling key.
func TestFreshnessWarnsFetchedButUnmerged(t *testing.T) {
	_, b := freshnessFixture(t)
	rawFetchTracking(t, b, "origin")

	for _, args := range [][]string{
		{"ready", "--ledger", "board"},
		{"show", "--ledger", "board"},
		{"status", "--ledger", "board"},
		{"status", "task-1", "--ledger", "board"},
	} {
		so, _, code := run(t, b, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, so)
		}
		doc := mustJSON(t, so)
		fresh, ok := doc["freshness"].(map[string]any)
		if !ok {
			t.Fatalf("%v: expected a freshness key: %v", args, doc)
		}
		if fresh["unmerged_remote_events"] != float64(1) {
			t.Fatalf("%v: expected 1 unmerged remote event: %v", args, fresh)
		}
		if fresh["hint"] != "run `ledger sync`" {
			t.Fatalf("%v: unexpected hint: %v", args, fresh)
		}
	}
}

// TestFreshnessWarnsFetchedButUnmergedTTY: the same fetched-but-unmerged
// state, on a TTY, prints the pinned stderr line — never inside the
// rendered lines the projection's TTY chrome carries.
func TestFreshnessWarnsFetchedButUnmergedTTY(t *testing.T) {
	_, b := freshnessFixture(t)
	rawFetchTracking(t, b, "origin")

	const want = "[ledger] 1 unmerged remote events — run 'ledger sync'"

	c, buf := ttyCtx(b)
	if err := runReady(c, "board", nil, 50); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("ready TTY: missing freshness stderr line: %q", buf.String())
	}

	c, buf = ttyCtx(b)
	if err := runShow(c, "board", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("show TTY: missing freshness stderr line: %q", buf.String())
	}

	c, buf = ttyCtx(b)
	if err := runStatus(c, "", "", false, "board"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("status TTY: missing freshness stderr line: %q", buf.String())
	}
}

// TestFreshnessSilentWhenSynced: once a replica actually runs `ledger sync`
// (merging the fetched events, not just fetching them), the tracking ref is
// an ancestor of local again and every read renders clean — no freshness
// key, no stderr line.
func TestFreshnessSilentWhenSynced(t *testing.T) {
	_, b := freshnessFixture(t)
	if got := syncResults(t, mustRun(t, b, "sync"), "synced"); got["board"]["result"] == "" {
		t.Fatalf("setup: expected a sync result: %+v", got)
	}

	for _, args := range [][]string{
		{"ready", "--ledger", "board"},
		{"show", "--ledger", "board"},
		{"status", "--ledger", "board"},
	} {
		so, _, code := run(t, b, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, so)
		}
		doc := mustJSON(t, so)
		if _, present := doc["freshness"]; present {
			t.Fatalf("%v: a fully-synced replica must render clean: %v", args, doc)
		}
	}

	c, buf := ttyCtx(b)
	if err := runReady(c, "board", nil, 50); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "[ledger]") {
		t.Fatalf("ready TTY: a synced replica must print no freshness line: %q", buf.String())
	}
}

// TestFreshnessSilentWithNoTrackingRef: a repo that has never synced or
// pushed carries no tracking ref at all — freshness degrades silently
// rather than erroring (the interface note: a read verb must never fail
// because there's nothing to check).
func TestFreshnessSilentWithNoTrackingRef(t *testing.T) {
	dir := initRepo(t)
	seedBoard(t, dir, "board")
	so, _, code := run(t, dir, "ready", "--ledger", "board")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	if _, present := doc["freshness"]; present {
		t.Fatalf("no remote/tracking ref: freshness must stay silent: %v", doc)
	}
}

// TestFreshnessSilentWithAmbiguousRemotes: two configured remotes with
// nothing selecting one is `ambiguous_remote` for sync/push, but a read verb
// has no --remote flag to disambiguate with and must never fail on it —
// freshness just has nothing to warn from.
func TestFreshnessSilentWithAmbiguousRemotes(t *testing.T) {
	dir := initRepo(t)
	git(t, dir, "remote", "add", "r1", "https://example.invalid/r1.git")
	git(t, dir, "remote", "add", "r2", "https://example.invalid/r2.git")
	seedBoard(t, dir, "board")
	so, _, code := run(t, dir, "ready", "--ledger", "board")
	if code != 0 {
		t.Fatalf("ambiguous remotes must not fail a read verb: %d %s", code, so)
	}
	doc := mustJSON(t, so)
	if _, present := doc["freshness"]; present {
		t.Fatalf("ambiguous remotes: freshness must stay silent, not guess which one: %v", doc)
	}
}

// TestFreshnessProjectionByteUnchanged: the spec's placement pin, verified
// directly — the freshness key's presence or absence must never change the
// projection's other members. Compares the exact same local state rendered
// before and after the tracking ref moves out from under it: b's local ref
// never changes between the two reads, only its (unmerged) tracking ref
// does, so stripping "freshness" from the warned document must reproduce
// the clean one byte for byte.
func TestFreshnessProjectionByteUnchanged(t *testing.T) {
	_, b := freshnessFixture(t)

	argSets := [][]string{
		{"ready", "--ledger", "board"},
		{"show", "--ledger", "board"},
		{"status", "--ledger", "board"},
	}
	clean := map[string]string{}
	for _, args := range argSets {
		so, _, code := run(t, b, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, so)
		}
		if strings.Contains(so, `"freshness"`) {
			t.Fatalf("%v: pre-fetch baseline must carry no freshness key: %s", args, so)
		}
		clean[args[0]] = so
	}

	rawFetchTracking(t, b, "origin")

	for _, args := range argSets {
		warned, _, code := run(t, b, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, warned)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(warned), &doc); err != nil {
			t.Fatalf("%v: payload not JSON: %v", args, err)
		}
		if _, present := doc["freshness"]; !present {
			t.Fatalf("%v: expected the warned run to carry freshness: %s", args, warned)
		}
		delete(doc, "freshness")
		strippedBytes, err := json.MarshalIndent(doc, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		stripped := string(strippedBytes) + "\n"

		var cleanDoc map[string]any
		json.Unmarshal([]byte(clean[args[0]]), &cleanDoc)
		cleanBytes, _ := json.MarshalIndent(cleanDoc, "", " ")
		cleanCanon := string(cleanBytes) + "\n"

		if stripped != cleanCanon {
			t.Fatalf("%v: projection changed by the freshness key's presence:\nclean:    %s\nstripped: %s",
				args, cleanCanon, stripped)
		}
	}
}

// TestFreshnessRootMismatchGuidance: two independently-created same-slug
// boards (TestSyncRefusesDifferentRoots' fixture, reused for the read
// side) — `ledger sync` fetches (populating the tracking ref) before it
// evaluates the same-root rule and refuses, so the tracking ref sits there
// after the refusal, holding a chain that shares no root with local. A read
// warns with the same export/import guidance sync's own refusal carries,
// never an unmerged-events count (there's nothing of THIS chain to merge).
func TestFreshnessRootMismatchGuidance(t *testing.T) {
	_, a, b := twoReplicas(t)
	seedBoardAs(t, a, "board", "alice")
	pushLedgerRef(t, a, "board")
	seedBoardAs(t, b, "board", "bob")

	if _, _, code := run(t, b, "sync"); code != 3 {
		t.Fatalf("setup: expected the root mismatch refusal (exit 3): got %d", code)
	}

	for _, args := range [][]string{
		{"ready", "--ledger", "board"},
		{"show", "--ledger", "board"},
		{"status", "--ledger", "board"},
	} {
		so, _, code := run(t, b, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, so)
		}
		doc := mustJSON(t, so)
		fresh, ok := doc["freshness"].(map[string]any)
		if !ok {
			t.Fatalf("%v: expected a freshness key with export/import guidance: %v", args, doc)
		}
		if _, present := fresh["unmerged_remote_events"]; present {
			t.Fatalf("%v: a root mismatch is not an unmerged-events count: %v", args, fresh)
		}
		hint, _ := fresh["hint"].(string)
		for _, want := range []string{"alice", "bob", "ledger export", "ledger import"} {
			if !strings.Contains(hint, want) {
				t.Fatalf("%v: guidance must name both creators and the export/import exit; missing %q in %q",
					args, want, hint)
			}
		}
	}

	c, buf := ttyCtx(b)
	if err := runShow(c, "board", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "export the local chain") {
		t.Fatalf("show TTY: missing root-mismatch guidance on stderr: %q", buf.String())
	}
}
