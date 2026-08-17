package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"ledger/internal/fold"
	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/store"
)

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("bad json: %v\n%s", err, s)
	}
	return doc
}

func TestCreateDefaultsAndEcho(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "create", "demo", "--scope", "test", "--as", "me")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	if doc["id"] == nil || doc["ledger"] != "demo" {
		t.Fatalf("create envelope: %v", doc)
	}
	fields := doc["fields"].(map[string]any)
	if _, ok := fields["status"]; !ok {
		t.Fatalf("default vocab missing: %v", fields)
	}
}

func TestCreateRejections(t *testing.T) {
	dir := initRepo(t)
	_, se, code := run(t, dir, "create", "UPPER", "--scope", "x")
	if code != 4 || !strings.Contains(se, "bad_slug") {
		t.Fatalf("%d %s", code, se)
	}
	run(t, dir, "create", "demo", "--scope", "x")
	_, se, code = run(t, dir, "create", "demo", "--scope", "x")
	if code != 4 || !strings.Contains(se, "slug_exists") {
		t.Fatalf("existing open slug: %s", se)
	}
	run(t, dir, "close", "demo", "--as-state", "abandoned")
	_, se, code = run(t, dir, "create", "demo", "--scope", "x")
	if code != 4 || !strings.Contains(se, "slug_exists") {
		t.Fatalf("closed slugs are never reused: %s", se)
	}
	_, se, _ = run(t, dir, "create", "d2", "--scope", "x", "--require-evidence", "review=approved")
	if !strings.Contains(se, "unknown_field") {
		t.Fatalf("require-evidence undeclared field: %s", se)
	}
}

func TestVocabAddAndClosedRules(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "demo", "--scope", "x", "--field", "status=open,done")
	so, _, code := run(t, dir, "vocab", "add", "demo", "status", "blocked", "-m", "needed")
	if code != 0 {
		t.Fatal(so)
	}
	run(t, dir, "close", "demo", "--as-state", "shipped")
	_, se, code := run(t, dir, "vocab", "add", "demo", "status", "later")
	if code != 4 || !strings.Contains(se, "closed") {
		t.Fatalf("vocab on closed: %s", se)
	}
}

// TestVocabAddOnFreeTextField: a nil-vocab (free-text) field takes any
// value already — vocab add on it isn't merely "not a declared field" (the
// unknown_field case for an undeclared field), it's a distinct, more
// specific message: there's no vocabulary to extend.
func TestVocabAddOnFreeTextField(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "demo", "--scope", "x", "--field", "notes=")
	_, se, code := run(t, dir, "vocab", "add", "demo", "notes", "whatever")
	if code != 4 || !strings.Contains(se, "unknown_field") || !strings.Contains(se, "is free-text and needs no vocabulary") {
		t.Fatalf("%d %s", code, se)
	}
}

// TestVocabAddRejectedOnReadyCapableStatus: a ready-capable board's status
// vocab is folded into board.Build's classification switch at declaration
// time — extending it live via `vocab add` would silently desync the two
// (the value lands in Schema, but the switch never learns about it), so
// this path must be closed off entirely rather than merely undocumented.
func TestVocabAddRejectedOnReadyCapableStatus(t *testing.T) {
	dir := setupReady(t)
	_, se, code := run(t, dir, "vocab", "add", "issues", "status", "blocked", "-m", "needed")
	if code != 4 {
		t.Fatalf("%d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_value" {
		t.Fatalf("want bad_value: %s", se)
	}
	if !strings.Contains(se, "immutable declaration") {
		t.Fatalf("message must name the all-or-nothing shape: %s", se)
	}
	if !strings.Contains(se, "open, in-progress, closed, wontfix") {
		t.Fatalf("hint must point at the declared vocab: %s", se)
	}
}

// TestVocabAddStillAllowedOnReadyCapableNonStatusField: the rejection is
// scoped to "status" specifically — a ready-capable board's other declared
// enum fields (never consulted by the frontier's classification switch)
// keep the ordinary extend-live behavior.
func TestVocabAddStillAllowedOnReadyCapableNonStatusField(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "create", "issues", "--scope", "test",
		"--field", "status=open,in-progress,closed,wontfix",
		"--field", "priority=low,high",
		"--terminal", "status=closed,wontfix",
		"--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by")
	if code != 0 {
		t.Fatal(so)
	}
	so, se, code := run(t, dir, "vocab", "add", "issues", "priority", "urgent", "-m", "needed")
	if code != 0 {
		t.Fatalf("%s %s", so, se)
	}
}

func TestCloseSupersededNeedsSuccessor(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	_, se, code := run(t, dir, "close", "old", "--as-state", "superseded")
	if code != 4 || !strings.Contains(se, "needs_successor") {
		t.Fatalf("%d %s", code, se)
	}
}

// TestCloseSupersededByRequiresSupersededState: --superseded-by only makes
// sense paired with --as-state superseded; any other state carrying it is
// a usage mistake, not a silently-ignored flag.
func TestCloseSupersededByRequiresSupersededState(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "demo", "--scope", "x")
	_, se, code := run(t, dir, "close", "demo", "--as-state", "abandoned", "--superseded-by", "ghost")
	if code != 4 || !strings.Contains(se, "bad_value") || !strings.Contains(se, "only superseded closes carry a successor") {
		t.Fatalf("%d %s", code, se)
	}
	// must not have mutated anything: the ledger stays open.
	so, _, _ := run(t, dir, "ls", "--all")
	rows := mustJSON(t, so)["ledgers"].([]any)
	if rows[0].(map[string]any)["state"] != "open" {
		t.Fatalf("rejected close must not mutate state: %s", so)
	}
}

// TestCloseSupersededSuccessorNotPresentLocally: closing as superseded with
// a successor slug that doesn't exist in this store yet must still succeed
// (the successor may be inbound via sync) but the payload and TTY output
// must carry a warning so the caller notices.
func TestCloseSupersededSuccessorNotPresentLocally(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	so, _, code := run(t, dir, "close", "old", "--as-state", "superseded", "--superseded-by", "ghost")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	warning, _ := doc["warning"].(string)
	if !strings.Contains(warning, "ghost") || !strings.Contains(warning, "not present locally") {
		t.Fatalf("expected a not-present-locally warning: %v", doc)
	}

	var buf strings.Builder
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}
	run(t, dir, "create", "old2", "--scope", "x")
	if err := runClose(c, "old2", "superseded", "ghost2", "", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "warning") || !strings.Contains(buf.String(), "ghost2") {
		t.Fatalf("TTY output must carry the warning line: %q", buf.String())
	}
}

func TestCloseBadState(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "demo", "--scope", "x")
	_, se, code := run(t, dir, "close", "demo", "--as-state", "banana")
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("%d %s", code, se)
	}
	// the bad value must not have written anything — the ledger stays open
	so, _, _ := run(t, dir, "ls", "--all")
	rows := mustJSON(t, so)["ledgers"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["state"] != "open" {
		t.Fatalf("bad --as-state must not mutate close state: %s", so)
	}
}

func TestCloseSupersededAtomic(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	run(t, dir, "create", "new", "--scope", "x")
	so, _, code := run(t, dir, "close", "old", "--as-state", "superseded", "--superseded-by", "new")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	if doc["id"] == nil || doc["close_id"] == nil {
		t.Fatalf("close+link payload must carry both ids (link event primary): %v", doc)
	}
	so, _, _ = run(t, dir, "ls", "--all")
	if !strings.Contains(so, "closed:superseded") {
		t.Fatalf("old must be closed:superseded: %s", so)
	}
}

func TestCreateSupersedes(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	so, _, code := run(t, dir, "create", "new", "--scope", "x", "--supersedes", "old")
	if code != 0 {
		t.Fatal(so)
	}
	// old is closed:superseded with a link to new (verified via status in Task 9;
	// here, assert through a raw read helper)
	so, _, _ = run(t, dir, "ls", "--all")
	if !strings.Contains(so, "old") || !strings.Contains(so, "new") {
		t.Fatal(so)
	}
}

func TestCreateSupersedesAlreadyClosed(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	run(t, dir, "close", "old", "--as-state", "abandoned")
	_, _, code := run(t, dir, "create", "recovery", "--scope", "x", "--supersedes", "old")
	if code != 0 {
		t.Fatal("supersede against an already-closed predecessor is the wrongful-close recovery and must work")
	}
}

// TestCreateSupersedesSuccessorSlugTaken: the successor slug already exists
// as an unrelated ledger (its meta.json doesn't name this predecessor) — a
// genuine collision, not a crash half-state, so it must fail exactly like
// plain create's slug_exists.
func TestCreateSupersedesSuccessorSlugTaken(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	run(t, dir, "create", "taken", "--scope", "x") // pre-existing, unrelated ledger
	_, se, code := run(t, dir, "create", "taken", "--scope", "x", "--supersedes", "old")
	if code != 4 || !strings.Contains(se, "slug_exists") {
		t.Fatalf("successor slug already taken by an unrelated ledger: %d %s", code, se)
	}
}

// TestCreateSupersedesCompletesDanglingLink manually stages the exact
// crash half-state createSuperseding must repair: the successor ref exists
// and its meta.json names the predecessor via Supersedes, but the
// predecessor never got its superseded_by link. Retrying `create
// --supersedes` must complete only the missing link and succeed.
func TestCreateSupersedesCompletesDanglingLink(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")

	s := store.Store{Repo: gitx.Repo{Dir: dir}}
	meta := model.Meta{Slug: "new", Scope: "x", Supersedes: "old",
		Fields:     map[string][]string{"status": {"open", "done", "failed", "blocked"}},
		FieldOrder: []string{"status"}}
	mb, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("new", model.Event{Type: "create", Author: "x"},
		map[string]string{"meta.json": string(mb)}, store.ExpectAbsent); err != nil {
		t.Fatal(err)
	}

	so, _, code := run(t, dir, "create", "new", "--scope", "x", "--supersedes", "old")
	if code != 0 {
		t.Fatal(so)
	}
	// recovery appends only the missing link event (per spec: it does not
	// synthesize a close event that never happened) — assert the redirect
	// directly via fold rather than through ls's state column.
	evs, m, err := s.Events("old")
	if err != nil {
		t.Fatal(err)
	}
	if led := fold.Fold("old", evs, m); led.SupersededBy != "new" {
		t.Fatalf("recovery must complete the predecessor's link: %+v", led)
	}
}
