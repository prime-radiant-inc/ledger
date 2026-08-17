package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ledger/internal/gitx"
	"ledger/internal/model"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	return dir
}

func testStore(t *testing.T) Store { return Store{Repo: gitx.Repo{Dir: initRepo(t)}} }

func mkEvent(author string) model.Event {
	return model.Event{TS: "2026-08-13T00:00:00", Type: "set", Key: "k",
		Fields: map[string]string{"status": "done"}, Author: author}
}

func TestAppendCreateReadRoundtrip(t *testing.T) {
	s := testStore(t)
	meta := `{"slug":"demo","scope":"x","fields":{"status":["open","done"]}}`
	id, err := s.Append("demo", model.Event{TS: "t", Type: "create", Author: "a"},
		map[string]string{"meta.json": meta}, ExpectAbsent)
	if err != nil || len(id) != 10 {
		t.Fatalf("create: %v %q", err, id)
	}
	if _, err := s.Append("demo", model.Event{Type: "create", Author: "a"}, nil, ExpectAbsent); err == nil {
		t.Fatal("second create must fail ErrSlugExists")
	}
	if _, err := s.Append("nope", mkEvent("a"), nil, ExpectPresent); err == nil {
		t.Fatal("append to missing ledger must fail")
	}
	id2, err := s.Append("demo", mkEvent("alice"), nil, ExpectPresent)
	if err != nil {
		t.Fatal(err)
	}
	evs, meta2, err := s.Events("demo")
	if err != nil || len(evs) != 2 || meta2.Slug != "demo" {
		t.Fatalf("events: %v n=%d meta=%+v", err, len(evs), meta2)
	}
	if evs[1].ID != id2 || evs[1].Author != "alice" || evs[1].Fields["status"] != "done" {
		t.Fatalf("read back: %+v", evs[1])
	}
	head, _ := s.HeadID("demo")
	if head != id2 {
		t.Fatalf("head %q != %q", head, id2)
	}
}

func TestSyntheticCommitIdentity(t *testing.T) {
	s := testStore(t)
	s.Append("demo", model.Event{Type: "create", Author: "roleX"},
		map[string]string{"meta.json": "{}"}, ExpectAbsent)
	out, _, _ := s.Repo.Git("", "log", "-1", "--format=%an|%ae|%cn|%ce", "refs/ledger/demo")
	parts := strings.Split(out, "|")
	if parts[0] != "roleX" || parts[1] != "author@ledger.invalid" || parts[3] != "marker@ledger.invalid" {
		t.Fatalf("identity: %q", out) // committer name = harness marker; never gitconfig
	}
}

func TestConcurrentAppendCAS(t *testing.T) {
	s := testStore(t)
	s.Append("demo", model.Event{Type: "create", Author: "a"},
		map[string]string{"meta.json": "{}"}, ExpectAbsent)
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, errs[i] = s.Append("demo", mkEvent("w"), nil, ExpectPresent) }(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	evs, _, _ := s.Events("demo")
	if len(evs) != 11 { // create + 10; no losses, no duplicates
		t.Fatalf("got %d events", len(evs))
	}
}

func TestResolveOrder(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("LEDGER_DIR", "")
	r, err := Resolve(repo) // explicit flag wins
	if err != nil || r.Store.Repo.Dir != repo || r.Note != "" {
		t.Fatalf("%v %+v", err, r)
	}
	other := initRepo(t)
	t.Setenv("LEDGER_DIR", other)
	r, _ = Resolve("")
	if r.Store.Repo.Dir != other {
		t.Fatalf("LEDGER_DIR should win over discovery: %q", r.Store.Repo.Dir)
	}
}

func TestResolveAncestorWalkUp(t *testing.T) {
	repo := initRepo(t)
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	t.Setenv("LEDGER_DIR", "")
	r, err := Resolve("")
	if err != nil || r.Store.Repo.Dir != repo || r.Note != "" {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestResolveSameDirCollision(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	ledgerGit := filepath.Join(dir, ".ledger.git")
	if err := os.Mkdir(ledgerGit, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("LEDGER_DIR", "")
	r, err := Resolve("")
	if err != nil || r.Store.Repo.Dir != ledgerGit || r.Note == "" {
		t.Fatalf("%v %+v", err, r)
	}
	if r.Shadowed != "" {
		t.Fatalf("a same-directory collision is the Note case, not a shadowed ancestor: %+v", r)
	}
}

// TestResolveShadowedAncestorStore is the field failure the eval reproduced:
// a misplaced `ledger init` left a bare .ledger.git above a project repo,
// every read from inside the repo resolved to the repo's own empty store,
// and nothing ever named the other one. Resolution still picks the repo —
// it now also reports the store it shadowed, either direction.
func TestResolveShadowedAncestorStore(t *testing.T) {
	t.Setenv("LEDGER_DIR", "")
	root := t.TempDir()
	bare := filepath.Join(root, ".ledger.git")
	if err := os.Mkdir(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Chdir(sub)
	r, err := Resolve("")
	if err != nil || r.Store.Repo.Dir != repo {
		t.Fatalf("the repo still wins: %v %+v", err, r)
	}
	if r.Shadowed != bare {
		t.Fatalf("shadowed ancestor store should be %q: %+v", bare, r)
	}

	// the other direction: a bare store nested inside a repo shadows the repo.
	nested := filepath.Join(repo, "scratch")
	nestedBare := filepath.Join(nested, ".ledger.git")
	if err := os.MkdirAll(nestedBare, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	r, err = Resolve("")
	if err != nil || r.Store.Repo.Dir != nestedBare {
		t.Fatalf("the nearest store still wins: %v %+v", err, r)
	}
	if r.Shadowed != repo {
		t.Fatalf("the repo above should be the shadowed store: %+v", r)
	}
}

// TestResolveShadowIsAmbientOnly: an explicit --store or $LEDGER_DIR is a
// choice already made — there is nothing to tell the caller about.
func TestResolveShadowIsAmbientOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".ledger.git"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "proj")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	t.Setenv("LEDGER_DIR", "")
	if r, _ := Resolve(repo); r.Shadowed != "" {
		t.Fatalf("--store says nothing about the ancestry: %+v", r)
	}
	t.Setenv("LEDGER_DIR", repo)
	if r, _ := Resolve(""); r.Shadowed != "" {
		t.Fatalf("$LEDGER_DIR says nothing about the ancestry: %+v", r)
	}
}

// TestResolveSameKindAncestorIsNotShadowing: a repo inside a repo (or a bare
// store under a bare store) is the ordinary nested-checkout case — the
// nearest one wins by design and a notice would be pure noise.
func TestResolveSameKindAncestorIsNotShadowing(t *testing.T) {
	t.Setenv("LEDGER_DIR", "")
	outer := t.TempDir()
	if err := os.Mkdir(filepath.Join(outer, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(inner)
	r, err := Resolve("")
	if err != nil || r.Store.Repo.Dir != inner || r.Shadowed != "" {
		t.Fatalf("nested repos must stay quiet: %v %+v", err, r)
	}
}

func TestTransactionAtomicity(t *testing.T) {
	s := testStore(t)
	s.Append("a", model.Event{Type: "create", Author: "x"}, map[string]string{"meta.json": "{}"}, ExpectAbsent)
	headA, _ := s.HeadID("a") // 10 chars; Transaction wants full shas — use rev-parse
	full, _, _ := s.Repo.Git("", "rev-parse", "refs/ledger/a")
	_ = headA
	// build a commit for ref b without updating any ref
	blob, _, _ := s.Repo.Git("{}", "hash-object", "-w", "--stdin")
	tree, _, _ := s.Repo.Git("100644 blob "+blob+"\tevent.json\n", "mktree")
	c1, _, _ := s.Repo.Git("", append(gitx.IdentityArgs("t", "terminal"), "commit-tree", tree, "-m", "x")...)
	// stale Old for ref a => whole transaction must abort; ref b must NOT be created
	err := s.Transaction([]TxStep{
		{Ref: "refs/ledger/b", New: c1, Old: ""},
		{Ref: "refs/ledger/a", New: c1, Old: strings.Repeat("0", 40)},
	})
	if err == nil {
		t.Fatal("stale CAS must abort")
	}
	if _, ok := s.head("b"); ok {
		t.Fatal("aborted transaction leaked ref b")
	}
	// correct Old commits both
	if err := s.Transaction([]TxStep{
		{Ref: "refs/ledger/b", New: c1, Old: ""},
		{Ref: "refs/ledger/a", New: c1, Old: full},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAppendCheckedPreconditionRunsFreshPerAttempt is the spec rule 7
// atomicity contract, minimal repro: pre must never work off a pre-loop
// snapshot. Attempt 1's pre invocation triggers a competing write (via
// sync.Once, so it fires exactly once) that lands after pre's own fresh
// read but before the CAS commit — the ref moves out from under attempt 1,
// forcing a retry. Attempt 2's pre invocation must see that competing
// write in its own fresh read.
func TestAppendCheckedPreconditionRunsFreshPerAttempt(t *testing.T) {
	s := testStore(t)
	s.Append("demo", model.Event{Type: "create", Author: "a"},
		map[string]string{"meta.json": "{}"}, ExpectAbsent)
	s.Append("demo", mkEvent("a"), nil, ExpectPresent) // event A

	var (
		mu              sync.Mutex
		calls           int
		lastSawSentinel bool
		once            sync.Once
	)
	pre := func(events []model.Event, reachedRoot bool) error {
		saw := false
		for _, ev := range events {
			if ev.Key == "sentinel" {
				saw = true
			}
		}
		mu.Lock()
		calls++
		lastSawSentinel = saw
		mu.Unlock()

		once.Do(func() {
			sentinel := model.Event{Type: "set", Key: "sentinel",
				Fields: map[string]string{"status": "done"}, Author: "competitor"}
			if _, err := s.Append("demo", sentinel, nil, ExpectPresent); err != nil {
				t.Fatalf("competing append: %v", err)
			}
		})
		return nil
	}

	evC := mkEvent("c")
	if _, err := s.AppendChecked("demo", &evC, pre, ExpectPresent); err != nil {
		t.Fatalf("AppendChecked: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Fatalf("pre ran %d time(s), want >=2 (a competing write mid-attempt must force a retry)", calls)
	}
	if !lastSawSentinel {
		t.Fatal("the last pre invocation must observe the sentinel written mid-retry — fresh read, not a pre-loop snapshot")
	}
}

// TestAppendCheckedPreconditionErrorAbortsNothingWritten covers the other
// half of the contract: a pre error aborts the append with that error, and
// nothing lands on the chain.
func TestAppendCheckedPreconditionErrorAbortsNothingWritten(t *testing.T) {
	s := testStore(t)
	s.Append("demo", model.Event{Type: "create", Author: "a"},
		map[string]string{"meta.json": "{}"}, ExpectAbsent)
	s.Append("demo", mkEvent("a"), nil, ExpectPresent)

	evsBefore, _, _ := s.Events("demo")

	wantErr := errors.New("precondition not met")
	pre := func(events []model.Event, reachedRoot bool) error { return wantErr }

	evC := mkEvent("c")
	if _, err := s.AppendChecked("demo", &evC, pre, ExpectPresent); !errors.Is(err, wantErr) {
		t.Fatalf("AppendChecked error = %v, want %v", err, wantErr)
	}

	evsAfter, _, _ := s.Events("demo")
	if len(evsAfter) != len(evsBefore) {
		t.Fatalf("pre error must write nothing: before=%d after=%d", len(evsBefore), len(evsAfter))
	}
}

// TestEventsFoldsAcrossDivergentBranches builds a real divergent chain (two
// branches off one root, joined by a sentinel sync merge) and pins the
// Kahn-fold read shape (spec Addition 1) end to end: Events must order both
// branches' events by timestamp across the whole chain, never by git's
// commit-build/traversal order, must never surface the sentinel, and must
// still read meta.json. EventsDAG must expose a single root and the
// contracted cross-sentinel Children edge from both branch tips forward to
// the event that follows the merge.
func TestEventsFoldsAcrossDivergentBranches(t *testing.T) {
	s := testStore(t)
	slug := "demo"

	metaJSON := `{"slug":"demo","scope":"x","fields":{"status":["open","done"]}}`
	rootEv := model.Event{TS: "2026-08-17T10:00:00.000", Type: "create", Author: "a"}
	rootSHA, err := s.BuildCommit(slug, "", rootEv, map[string]string{"meta.json": metaJSON})
	if err != nil {
		t.Fatal(err)
	}

	// Branch A is built first but timestamped LATER than branch B — the
	// fold must key off (ts, sha), never off build/traversal order.
	aEv := model.Event{TS: "2026-08-17T12:00:00.000", Type: "set", Key: "a1", Author: "alice"}
	aSHA, err := s.BuildCommit(slug, rootSHA, aEv, nil)
	if err != nil {
		t.Fatal(err)
	}
	bEv := model.Event{TS: "2026-08-17T11:00:00.000", Type: "set", Key: "b1", Author: "bob"}
	bSHA, err := s.BuildCommit(slug, rootSHA, bEv, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A sentinel sync merge joining the two branches: never dropped from the
	// DAG, but contracted out before the sort — it must never appear in
	// Events and must never delay or reorder a real event.
	blob, _, code := s.Repo.Git(`{"type":"sync","ts":"2026-08-17T23:00:00.000","author":"host"}`,
		"hash-object", "-w", "--stdin")
	if code != 0 {
		t.Fatal("hash-object for sentinel failed")
	}
	tree, _, code := s.Repo.Git("100644 blob "+blob+"\tevent.json\n", "mktree")
	if code != 0 {
		t.Fatal("mktree for sentinel failed")
	}
	mergeSHA, _, code := s.Repo.Git("", append(gitx.IdentityArgs("host", "sync"),
		"commit-tree", tree, "-p", aSHA, "-p", bSHA, "-m", "sync")...)
	if code != 0 {
		t.Fatal("commit-tree for sentinel failed")
	}

	afterEv := model.Event{TS: "2026-08-17T13:00:00.000", Type: "set", Key: "after", Author: "carol"}
	afterSHA, err := s.BuildCommit(slug, mergeSHA, afterEv, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, code := s.Repo.Git("", "update-ref", ref(slug), afterSHA); code != 0 {
		t.Fatal("update-ref failed")
	}

	evs, meta, err := s.Events(slug)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var keys []string
	for _, ev := range evs {
		keys = append(keys, ev.Type+":"+ev.Key)
	}
	want := []string{"create:", "set:b1", "set:a1", "set:after"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("fold order = %v, want %v (b1's earlier ts must win despite a1 being built first)", keys, want)
	}
	for _, ev := range evs {
		if ev.Type == "sync" {
			t.Fatal("a sentinel sync event must never appear in Events")
		}
	}
	if meta.Slug != "demo" {
		t.Fatalf("meta not read: %+v", meta)
	}

	_, _, result, err := s.EventsDAG(slug)
	if err != nil {
		t.Fatalf("EventsDAG: %v", err)
	}
	if len(result.Roots) != 1 {
		t.Fatalf("roots = %v, want a single root", result.Roots)
	}
	if !model.Contains(result.Children[aSHA], afterSHA) || !model.Contains(result.Children[bSHA], afterSHA) {
		t.Fatalf("Children must carry the cross-sentinel edge from both branch tips to after: %v", result.Children)
	}
}

func TestCatBatchTrailingNewlinePreserved(t *testing.T) {
	s := testStore(t)
	blob1, _, code := s.Repo.Git("no trailing newline here", "hash-object", "-w", "--stdin")
	if code != 0 {
		t.Fatal("hash-object failed for blob1")
	}
	content2 := "trailing newline content\n"
	blob2, _, code := s.Repo.Git(content2, "hash-object", "-w", "--stdin")
	if code != 0 {
		t.Fatal("hash-object failed for blob2")
	}
	contents, present := s.catBatch([]string{blob1, blob2})
	if !present[1] || contents[1] != content2 {
		t.Fatalf("last blob truncated: present=%v got %q want %q", present[1], contents[1], content2)
	}
}
