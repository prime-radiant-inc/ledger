package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- `title` is a reserved field name (bridge design rev 6) ----

// TestTitleIsReservedAtCreate: no board may declare, guard, or extend a
// field called "title". A legal board could otherwise declare and guard
// one, splitting the contested read path (which unions the rename stream
// and any guarded field) from the write path (renames only).
func TestTitleIsReservedAtCreate(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"--field", []string{"create", "b1", "--scope", "s", "--field", "title=a,b"}},
		{"--multi-field", []string{"create", "b2", "--scope", "s", "--field", "status=open,done", "--multi-field", "title"}},
		{"--guard", []string{"create", "b3", "--scope", "s", "--field", "status=open,done", "--guard", "title"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := initRepo(t)
			_, se, code := run(t, dir, c.args...)
			if code != 4 {
				t.Fatalf("%s title must be refused: exit %d, stderr %s", c.name, code, se)
			}
			doc := mustJSON(t, se)
			if doc["error"] != "bad_value" {
				t.Fatalf("%s title must be bad_value: %s", c.name, se)
			}
			if !strings.Contains(doc["message"].(string), "reserved") {
				t.Fatalf("%s title's message must say reserved: %s", c.name, se)
			}
		})
	}
}

// TestTitleIsReservedAtVocab: `vocab add <slug> title <value>` is refused
// as reserved too — the same ruling, on the one verb that can extend a
// declared field after create.
func TestTitleIsReservedAtVocab(t *testing.T) {
	dir := setupReady(t)
	_, se, code := run(t, dir, "vocab", "add", "issues", "title", "anything", "-m", "why")
	if code != 4 {
		t.Fatalf("vocab add title must be refused: exit %d, stderr %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_value" || !strings.Contains(doc["message"].(string), "reserved") {
		t.Fatalf("vocab add title must be a reserved bad_value: %s", se)
	}
}

// TestTitleIsReservedAtImport: import re-validates the exported meta, so a
// hand-edited export declaring "title" is refused at the boundary rather
// than recreated as a board no later create could ever have made.
func TestTitleIsReservedAtImport(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "a title", "--as", "a")

	path := filepath.Join(t.TempDir(), "export.jsonl")
	if _, se, code := run(t, dir, "export", "issues", "--to", path); code != 0 {
		t.Fatalf("export: %s", se)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(data), "\n", 2)
	var header map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(header["meta"], &meta); err != nil {
		t.Fatal(err)
	}
	meta["multi_fields"] = json.RawMessage(`["labels","blocked-by","title"]`)
	mb, _ := json.Marshal(meta)
	header["meta"] = mb
	hb, _ := json.Marshal(header)
	edited := filepath.Join(t.TempDir(), "titled.jsonl")
	if err := os.WriteFile(edited, []byte(string(hb)+"\n"+lines[1]), 0o644); err != nil {
		t.Fatal(err)
	}

	_, se, code := run(t, dir, "import", edited, "--slug", "copy")
	if code != 4 {
		t.Fatalf("importing a board declaring 'title' must be refused: exit %d, stderr %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "bad_value" || !strings.Contains(doc["message"].(string), "reserved") {
		t.Fatalf("import must refuse 'title' as reserved: %s", se)
	}
}

// ---- the write path: `ledger set <key> --rename "<new title>"` ----

// seedKey seeds one titled key on a ready-capable board and returns the
// seed event's id.
func seedKey(t *testing.T, dir, key, title, as string) string {
	t.Helper()
	so := mustRun(t, dir, "set", key, "status=open", "--expect", "none", "-m", title, "--as", as)
	return mustJSON(t, so)["id"].(string)
}

// renamedInfo pulls the `renamed` structure off a JSON row/entry.
func renamedInfo(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	info, ok := m["renamed"].(map[string]any)
	if !ok {
		t.Fatalf("expected a renamed structure: %+v", m)
	}
	return info
}

// TestRenameLandsAndRetitlesEveryReadingSurface: the write path end to end —
// the response echoes prior and new title, `show` rows and the `ready`
// envelope carry the new title with its renamed label, and the seed message
// is never resurrected.
func TestRenameLandsAndRetitlesEveryReadingSurface(t *testing.T) {
	dir := setupReady(t)
	seedKey(t, dir, "k1", "fix teh retry lop", "ash")

	so := mustRun(t, dir, "set", "k1", "--rename", "fix the retry loop", "--as", "kit")
	doc := mustJSON(t, so)
	if doc["key"] != "k1" || doc["rename"] != "fix the retry loop" {
		t.Fatalf("rename response: %s", so)
	}
	if doc["prior_title"] != "fix teh retry lop" {
		t.Fatalf("the response names the title it replaced: %s", so)
	}
	renameID := doc["id"].(string)

	showDoc := mustJSON(t, mustRun(t, dir, "show"))
	for _, r := range showDoc["rows"].([]any) {
		row := r.(map[string]any)
		if row["key"] != "k1" {
			continue
		}
		if row["title"] != "fix the retry loop" {
			t.Fatalf("show rows carry the renamed title: %+v", row)
		}
		info := renamedInfo(t, row)
		if info["by"] != "kit" || info["id"] != renameID {
			t.Fatalf("show rows carry attribution: %+v", info)
		}
		prior := info["prior"].([]any)
		if len(prior) != 1 || prior[0] != "fix teh retry lop" {
			t.Fatalf("prior carries the fold-path seed: %+v", info)
		}
	}

	readyDoc := mustJSON(t, mustRun(t, dir, "ready", "--ledger", "issues"))
	entry := readyDoc["ready"].([]any)[0].(map[string]any)
	if entry["title"] != "fix the retry loop" {
		t.Fatalf("ready entries carry the renamed title: %+v", entry)
	}
	if renamedInfo(t, entry)["by"] != "kit" {
		t.Fatalf("ready entries carry the renamed label: %+v", entry)
	}
}

// TestRenameBadUsageMatrix: one assertion per event. A rename carries no
// field assignments and no evidence; a bare rename carries no -m (the title
// is --rename's own argument); and the title itself is one non-empty line.
func TestRenameBadUsageMatrix(t *testing.T) {
	dir := setupReady(t)
	seedKey(t, dir, "k1", "the title", "ash")

	for _, c := range []struct {
		name string
		args []string
		code string
	}{
		{"with a field assignment", []string{"set", "k1", "status=in-progress", "--rename", "new title", "--as", "kit"}, "bad_usage"},
		{"with evidence", []string{"set", "k1", "--rename", "new title", "--evidence", "commit:abc123", "--as", "kit"}, "bad_usage"},
		{"bare rename with -m", []string{"set", "k1", "--rename", "new title", "-m", "why", "--as", "kit"}, "bad_usage"},
		{"empty title", []string{"set", "k1", "--rename", "   ", "--as", "kit"}, "empty_body"},
		{"multi-line title", []string{"set", "k1", "--rename", "line one\nline two", "--as", "kit"}, "bad_value"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, se, code := run(t, dir, c.args...)
			if code != 4 {
				t.Fatalf("must be refused: exit %d, %s", code, se)
			}
			if got := mustJSON(t, se)["error"]; got != c.code {
				t.Fatalf("want %s, got %v: %s", c.code, got, se)
			}
		})
	}
}

// TestRenameRefusedOnPlainBoardAndUnknownKey: existence. Titles live only on
// ready-capable boards, and a rename needs a locally existing titled key —
// the hint is the seed command.
func TestRenameRefusedOnPlainBoardAndUnknownKey(t *testing.T) {
	plain := setup(t)
	_, se, code := run(t, plain, "set", "t1", "--rename", "a title", "--as", "kit")
	if code != 4 || mustJSON(t, se)["error"] != "bad_usage" {
		t.Fatalf("a plain board has no titles to rename: %d %s", code, se)
	}

	dir := setupReady(t)
	seedKey(t, dir, "k1", "the title", "ash")
	_, se, code = run(t, dir, "set", "nosuch", "--rename", "a title", "--as", "kit")
	if code != 4 {
		t.Fatalf("unknown key must be refused: %d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "unknown_key" {
		t.Fatalf("want unknown_key: %s", se)
	}
	if !strings.Contains(doc["hint"].(string), "set nosuch status=open --expect none") {
		t.Fatalf("the hint is the seed command: %s", se)
	}

	// A key with labels but no status write has no title either.
	mustRun(t, dir, "set", "halfseed", "labels=urgent", "--as", "ash")
	_, se, code = run(t, dir, "set", "halfseed", "--rename", "a title", "--as", "kit")
	if code != 4 || mustJSON(t, se)["error"] != "unknown_key" {
		t.Fatalf("a statusless, never-renamed key has no title to rename: %d %s", code, se)
	}
}

// ---- the gate: human gates a rename, claim and settled do not ----

// TestRenameHumanGateNeedsOverrideAndRecordsIt: retitling a person's
// reserved issue under them is the friction the human label exists to
// create. The refusal carries machine-readable signals[]; the override lands
// with `override: human` recorded and its -m rendering as override text,
// never as a title.
func TestRenameHumanGateNeedsOverrideAndRecordsIt(t *testing.T) {
	dir := setupReady(t)
	seedKey(t, dir, "design-review", "pick the retry API shape", "ash")
	mustRun(t, dir, "set", "design-review", "labels=human", "--expect", "none", "--as", "ash")

	_, se, code := run(t, dir, "set", "design-review", "--rename", "pick the retry contract", "--as", "kit")
	if code != 4 {
		t.Fatalf("a human-labeled key must gate the rename: %d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "needs_override" {
		t.Fatalf("want needs_override: %s", se)
	}
	if !strings.Contains(doc["message"].(string), "human (labeled 'human')") {
		t.Fatalf("the message names the signal: %s", se)
	}
	signals, ok := doc["signals"].([]any)
	if !ok || len(signals) != 1 || signals[0] != "human" {
		t.Fatalf("needs_override must carry machine-readable signals[]: %s", se)
	}

	so := mustRun(t, dir, "set", "design-review", "--rename", "pick the retry contract",
		"--override", "-m", "jesse asked for the retitle in standup", "--as", "kit")
	renameID := mustJSON(t, so)["id"].(string)

	raw := mustRun(t, dir, "tail", "--raw", "-n", "5", "--ledger", "issues")
	if !strings.Contains(raw, `"override": "human"`) {
		t.Fatalf("the chain must record override: human:\n%s", raw)
	}
	ev := mustJSON(t, mustRun(t, dir, "show", "--id", renameID, "--ledger", "issues"))
	if ev["text"] != "jesse asked for the retitle in standup" {
		t.Fatalf("the -m rides the event as the override justification: %+v", ev)
	}
	if ev["rename"] != "pick the retry contract" {
		t.Fatalf("the title comes from --rename, never from -m: %+v", ev)
	}
	k := mustJSON(t, mustRun(t, dir, "show"))["rows"].([]any)
	for _, r := range k {
		row := r.(map[string]any)
		if row["key"] == "design-review" && row["title"] != "pick the retry contract" {
			t.Fatalf("the override's -m must never become the title: %+v", row)
		}
	}
}

// TestRenameNotGatedByClaimOrSettled: a title is not an outcome. Another
// author's live claim, and a settled (terminal) key, both leave a rename
// ungated.
func TestRenameNotGatedByClaimOrSettled(t *testing.T) {
	dir := setupReady(t)
	seedID := seedKey(t, dir, "k1", "the title", "ash")
	claimSo := mustRun(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "alice")
	claimID := mustJSON(t, claimSo)["id"].(string)

	if _, se, code := run(t, dir, "set", "k1", "--rename", "a better title", "--as", "kit"); code != 0 {
		t.Fatalf("a live cross-author claim must not gate a rename: %d %s", code, se)
	}

	mustRun(t, dir, "set", "k1", "status=closed", "--evidence", "commit:abc123", "--expect", claimID, "-m", "done", "--as", "alice")
	if _, se, code := run(t, dir, "set", "k1", "--rename", "the settled title", "--as", "kit"); code != 0 {
		t.Fatalf("a settled key must not gate a rename: %d %s", code, se)
	}
}

// TestRenameOverrideWithNoStandingSignalIsLegalNoOp: --override on a rename
// with nothing standing is legal and records no override — the same ruling
// as every other write verb. `human` is an unguarded labels token any writer
// or sync merge can clear, so a bad_usage here would be a mid-CAS-loop
// TOCTOU.
func TestRenameOverrideWithNoStandingSignalIsLegalNoOp(t *testing.T) {
	dir := setupReady(t)
	seedKey(t, dir, "k1", "the title", "ash")

	so, se, code := run(t, dir, "set", "k1", "--rename", "a better title",
		"--override", "-m", "override unnecessary here", "--as", "kit")
	if code != 0 {
		t.Fatalf("--override with no standing signal must still land: %d %s", code, se)
	}
	id := mustJSON(t, so)["id"].(string)

	for _, e := range mustJSON(t, mustRun(t, dir, "tail", "--raw", "--ledger", "issues"))["events"].([]any) {
		ev := e.(map[string]any)
		if ev["id"] != id {
			continue
		}
		if _, has := ev["override"]; has {
			t.Fatalf("a legal no-op must record no override: %+v", ev)
		}
		return
	}
	t.Fatalf("the rename event must be on the chain: %s", so)
}

// ---- the rename-scoped CAS stream ----

// TestRenameExpectCAS: --expect on a rename is CAS against the key's latest
// RENAME event — a second, rename-scoped stream alongside field-scoped CAS.
func TestRenameExpectCAS(t *testing.T) {
	dir := setupReady(t)
	seedID := seedKey(t, dir, "k1", "the title", "ash")

	// --expect none on a never-renamed key: the first rename's form.
	so, se, code := run(t, dir, "set", "k1", "--rename", "first rename", "--expect", "none", "--as", "kit")
	if code != 0 {
		t.Fatalf("--expect none must succeed on a never-renamed key: %d %s", code, se)
	}
	firstID := mustJSON(t, so)["id"].(string)

	// --expect none once renamed: claim_lost, naming the rename that won.
	_, se, code = run(t, dir, "set", "k1", "--rename", "second rename", "--expect", "none", "--as", "kit")
	if code != 4 {
		t.Fatalf("--expect none on a renamed key must lose: %d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "claim_lost" {
		t.Fatalf("want claim_lost: %s", se)
	}
	wantMsg := `event ` + firstID + ` by kit already renamed 'k1' to "first rename"`
	if doc["message"] != wantMsg {
		t.Fatalf("pinned claim_lost message:\n got %q\nwant %q", doc["message"], wantMsg)
	}
	if !strings.Contains(doc["hint"].(string), "read the current title first") {
		t.Fatalf("pinned hint: %s", se)
	}

	// A stale id loses; the current one wins.
	if _, se, code = run(t, dir, "set", "k1", "--rename", "stale attempt", "--expect", seedID, "--as", "kit"); code != 4 {
		t.Fatalf("a status event id is not a rename id — must lose: %d %s", code, se)
	}
	so, se, code = run(t, dir, "set", "k1", "--rename", "second rename", "--expect", firstID, "--as", "kit")
	if code != 0 {
		t.Fatalf("--expect <latest rename id> must win: %d %s", code, se)
	}
	if mustJSON(t, so)["prior_title"] != "first rename" {
		t.Fatalf("prior_title: %s", so)
	}

	// An id on a key that was never renamed at all names no winner.
	seedKey(t, dir, "k2", "k2's title", "ash")
	_, se, code = run(t, dir, "set", "k2", "--rename", "nope", "--expect", firstID, "--as", "kit")
	if code != 4 || !strings.Contains(se, "has never been renamed") {
		t.Fatalf("--expect <id> on a never-renamed key must name the actual state: %d %s", code, se)
	}

	// The syntax floor applies on this stream too.
	if _, se, code = run(t, dir, "set", "k1", "--rename", "nope", "--expect", "", "--as", "kit"); code != 4 ||
		!strings.Contains(se, "bad_usage") {
		t.Fatalf("an empty --expect must fail closed on the rename stream too: %d %s", code, se)
	}
}

// TestRenameIdempotencyIsSymmetric: rename events dedupe only against rename
// events, and field writes only against field-carrying events — a field
// write never dedupes against a rename sharing its (author, key, idem), and
// vice versa.
func TestRenameIdempotencyIsSymmetric(t *testing.T) {
	dir := setupReady(t)
	seedKey(t, dir, "k1", "the title", "ash")

	so := mustRun(t, dir, "set", "k1", "--rename", "a better title", "--idempotency-key", "abc", "--as", "kit")
	renameID := mustJSON(t, so)["id"].(string)

	// rename vs rename: the retry dedupes.
	again := mustJSON(t, mustRun(t, dir, "set", "k1", "--rename", "a better title", "--idempotency-key", "abc", "--as", "kit"))
	if again["deduped"] != true || again["id"] != renameID {
		t.Fatalf("a repeated rename under the same key must dedupe: %+v", again)
	}

	// field write vs rename: never deduped — it asserts something else.
	fieldSo := mustRun(t, dir, "set", "k1", "labels=urgent", "--idempotency-key", "abc", "--as", "kit")
	fieldDoc := mustJSON(t, fieldSo)
	if fieldDoc["deduped"] == true {
		t.Fatalf("a field write must never dedupe against a rename: %s", fieldSo)
	}
	fieldID := fieldDoc["id"].(string)

	// rename vs field write, the mirror image: a NEW idem key used first by a
	// field write must not swallow a rename.
	mustRun(t, dir, "set", "k1", "labels=urgent,triage", "--idempotency-key", "xyz", "--as", "kit")
	renameSo := mustRun(t, dir, "set", "k1", "--rename", "a third title", "--idempotency-key", "xyz", "--as", "kit")
	renameDoc := mustJSON(t, renameSo)
	if renameDoc["deduped"] == true {
		t.Fatalf("a rename must never dedupe against a field write: %s", renameSo)
	}
	if renameDoc["id"] == fieldID {
		t.Fatalf("the rename must be its own event: %s", renameSo)
	}
}

// ---- concurrent renames, through a real sync ----

// renameReplicas builds a real two-replica TITLE race against real git: both
// sides seed from the same board, then rename task-1 concurrently, then
// replica b merges a's side in.
func renameReplicas(t *testing.T) (a, b string) {
	t.Helper()
	_, a, b = twoReplicas(t)
	seedBoard(t, a, "board")
	mustRun(t, a, "set", "task-1", "status=open", "--expect", "none", "-m", "the seed title", "--as", "seeder")
	pushLedgerRef(t, a, "board")
	mustRun(t, b, "sync")

	mustRun(t, a, "set", "task-1", "--rename", "alice's title", "--as", "alice")
	mustRun(t, b, "set", "task-1", "--rename", "bob's title", "--as", "bob")

	pushLedgerRef(t, a, "board")
	mustRun(t, b, "sync")
	return a, b
}

// TestConcurrentRenamesConvergeAndContest: two replicas rename the same key
// during a partition; the merge converges on ONE title (last in fold order),
// the loser stays greppable in the chain, and the race surfaces as a
// contested attention entry on the title pseudo-field — attention only.
func TestConcurrentRenamesConvergeAndContest(t *testing.T) {
	_, b := renameReplicas(t)

	so := mustRun(t, b, "ready", "--ledger", "board")
	doc := mustJSON(t, so)
	entries := contestedEntries(t, so)
	if len(entries) != 1 {
		t.Fatalf("want exactly one contested entry:\n%s", so)
	}
	c := entries[0]["contest"].(map[string]any)
	if c["field"] != "title" {
		t.Fatalf("the rename stream contests as the pseudo-field 'title': %+v", c)
	}
	ids := c["ids"].([]any)
	if len(ids) != 2 || c["expect"] != ids[1] {
		t.Fatalf("two heads, winner last, expect = the winner: %+v", c)
	}

	// The converged title IS the contest winner, and both entries agree.
	title := entries[0]["title"].(string)
	ready := doc["ready"].([]any)
	if len(ready) != 1 || ready[0].(map[string]any)["title"] != title {
		t.Fatalf("one title per key per envelope: %s", so)
	}

	// Attention only: the ready entry is not flagged contested, and the
	// frontier still reports pickable work rather than being held.
	if ready[0].(map[string]any)["contested"] == true {
		t.Fatalf("a title contest must not flag the entry: %s", so)
	}
	if doc["frontier"] != "work-available" {
		t.Fatalf("frontier: %v", doc["frontier"])
	}

	// The loser is greppable forever: its rename event is in the raw chain.
	raw := mustRun(t, b, "tail", "--raw", "-n", "0", "--ledger", "board")
	loser := "alice's title"
	if title == "alice's title" {
		loser = "bob's title"
	}
	if !strings.Contains(raw, loser) {
		t.Fatalf("the losing rename must stay in the chain:\n%s", raw)
	}
}

// TestTitleContestAloneKeepsTheFrontierAllHandled: the attention-only ruling
// where it bites — a board whose only unhandled fact is a title race must
// not hold a fleet in the picking loop.
func TestTitleContestAloneKeepsTheFrontierAllHandled(t *testing.T) {
	_, b := renameReplicas(t)
	// Close the key so nothing else is pickable or open: only the title
	// contest is left.
	openID := statusID(t, b, "board", "task-1")
	mustRun(t, b, "set", "task-1", "status=done", "--expect", openID, "-m", "done", "--as", "bob")

	doc := mustJSON(t, mustRun(t, b, "ready", "--ledger", "board"))
	if doc["frontier"] != "all-handled" {
		t.Fatalf("a title contest alone must not move the verdict: %s", doc["frontier"])
	}
	totals := doc["totals"].(map[string]any)
	if totals["attention"].(float64) != 1 {
		t.Fatalf("the entry is still listed and still counted: %+v", totals)
	}
}

// TestTitleCollapseIdiomWorksVerbatim: the collapse idiom is
// `set <key> --rename "<keeper>" --expect <contest.expect>` — no --override
// (settled never gates a rename) — and it records the losing head.
func TestTitleCollapseIdiomWorksVerbatim(t *testing.T) {
	_, b := renameReplicas(t)
	entries := contestedEntries(t, mustRun(t, b, "ready", "--ledger", "board"))
	c := entries[0]["contest"].(map[string]any)
	expect := c["expect"].(string)
	loser := c["ids"].([]any)[0].(string)

	so, se, code := run(t, b, "set", "task-1", "--rename", "the agreed title", "--expect", expect, "--as", "kit")
	if code != 0 {
		t.Fatalf("the collapse idiom must work verbatim: %d %s", code, se)
	}
	got, ok := mustJSON(t, so)["contested_resolved"].([]any)
	if !ok || len(got) != 1 || got[0] != loser {
		t.Fatalf("the collapsing rename must record the losing head: %s", so)
	}
	if e := contestedEntries(t, mustRun(t, b, "ready", "--ledger", "board")); len(e) != 0 {
		t.Fatalf("the collapse clears the contest: %+v", e)
	}
	doc := mustJSON(t, mustRun(t, b, "ready", "--ledger", "board"))
	if doc["ready"].([]any)[0].(map[string]any)["title"] != "the agreed title" {
		t.Fatalf("the keeper title stands: %s", so)
	}
}
