package store

import (
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
	st, note, err := Resolve(repo) // explicit flag wins
	if err != nil || st.Repo.Dir != repo || note != "" {
		t.Fatalf("%v %+v %q", err, st, note)
	}
	other := initRepo(t)
	t.Setenv("LEDGER_DIR", other)
	st, _, _ = Resolve("")
	if st.Repo.Dir != other {
		t.Fatalf("LEDGER_DIR should win over discovery: %q", st.Repo.Dir)
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
	st, note, err := Resolve("")
	if err != nil || st.Repo.Dir != repo || note != "" {
		t.Fatalf("%v %+v %q", err, st, note)
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
	st, note, err := Resolve("")
	if err != nil || st.Repo.Dir != ledgerGit || note == "" {
		t.Fatalf("%v %+v %q", err, st, note)
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
