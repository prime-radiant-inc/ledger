package cmd

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ledger/internal/fold"
	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/store"
)

func jsonEq(t *testing.T, a, b any) bool {
	t.Helper()
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// writeEv appends a set event and returns its id.
func writeEv(t *testing.T, dir, key string) string {
	t.Helper()
	so, se, code := run(t, dir, "set", key, "status=open", "-m", "work", "--as", "w")
	if code != 0 {
		t.Fatalf("set failed: %s %s", so, se)
	}
	return mustJSON(t, so)["id"].(string)
}

func TestRollupBareShowsRootsAndGrammar(t *testing.T) {
	dir := setup(t)
	writeEv(t, dir, "k1")
	so, se, code := run(t, dir, "rollup")
	if code != 0 {
		t.Fatalf("bare rollup must exit 0: %s", se)
	}
	doc := mustJSON(t, so)
	if doc["rollup_due"] == nil || doc["roots"] == nil {
		t.Fatalf("bare rollup payload missing roots/rollup_due: %v", doc)
	}
	// F1: the submit grammar and curation discipline must ride the JSON
	// payload itself — non-TTY is the default shape, and an agent driving it
	// never sees the TTY-only lines.
	guidanceAny, ok := doc["guidance"].([]any)
	if !ok || len(guidanceAny) == 0 {
		t.Fatalf("bare rollup payload missing non-empty guidance array: %v", doc)
	}
	var guidance []string
	for _, g := range guidanceAny {
		s, ok := g.(string)
		if !ok {
			t.Fatalf("guidance entries must be strings: %v", doc)
		}
		guidance = append(guidance, s)
	}
	if !strings.Contains(strings.Join(guidance, "\n"), `ledger rollup <id> <id> ... -m`) {
		t.Fatalf("guidance must carry the literal submit grammar: %v", guidance)
	}
}

func TestRollupSubmitAndErrors(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	b := writeEv(t, dir, "k2")

	// happy path
	so, se, code := run(t, dir, "rollup", a, b, "-m", "thread done: k1+k2 landed", "--as", "curator")
	if code != 0 {
		t.Fatalf("submit failed: %s", se)
	}
	doc := mustJSON(t, so)
	rid := doc["id"].(string)
	if doc["children"].(float64) != 2 {
		t.Fatalf("children count: %v", doc)
	}
	if _, ok := doc["rollup_due"]; !ok {
		t.Fatalf("no rollup_due in envelope: %v", doc)
	}

	// child_taken names the owning rollup and hints inclusion
	_, se, code = run(t, dir, "rollup", a, "-m", "dup", "--as", "curator")
	if code != 4 || !strings.Contains(se, "child_taken") || !strings.Contains(se, rid) {
		t.Fatalf("child_taken must name owner %s: %d %s", rid, code, se)
	}
	errDoc := mustJSON(t, se)
	if errDoc["error"] != "child_taken" {
		t.Fatalf("child_taken error field: %v", errDoc)
	}
	hint, _ := errDoc["hint"].(string)
	if !strings.Contains(hint, rid) {
		t.Fatalf("child_taken hint (not just message) must name owning rollup %s: %v", rid, errDoc)
	}

	// unknown_event: the id must genuinely be absent from the raw chain
	badID := "deadbeef00"
	_, se, code = run(t, dir, "rollup", badID, "-m", "x", "--as", "curator")
	if code != 4 || !strings.Contains(se, "unknown_event") {
		t.Fatalf("unknown_event: %d %s", code, se)
	}
	rawSO, _, _ := run(t, dir, "tail", "--raw", "-n", "50")
	rawDoc := mustJSON(t, rawSO)
	for _, e := range rawDoc["events"].([]any) {
		if e.(map[string]any)["id"] == badID {
			t.Fatalf("test setup bug: %s unexpectedly present in raw chain: %s", badID, rawSO)
		}
	}

	// empty and multi-line summaries refused
	_, se, code = run(t, dir, "rollup", rid, "--as", "curator")
	if code != 4 || !strings.Contains(se, "empty_body") {
		t.Fatalf("empty_body: %d %s", code, se)
	}
	_, se, code = run(t, dir, "rollup", rid, "-m", "two\nlines", "--as", "curator")
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("multi-line must be bad_value: %d %s", code, se)
	}

	// recursion: rolling the rollup works (correction idiom)
	so, se, code = run(t, dir, "rollup", rid, "-m", "corrected line", "--as", "resumer")
	if code != 0 {
		t.Fatalf("recursive rollup failed: %s", se)
	}
}

// TestRollupSummarySizeBound: F2 — a rollup summary over 300 bytes is
// refused at write with bad_value; exactly 300 bytes is accepted.
func TestRollupSummarySizeBound(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	b := writeEv(t, dir, "k2")

	tooLong := strings.Repeat("x", 301)
	_, se, code := run(t, dir, "rollup", a, "-m", tooLong, "--as", "curator")
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("301-byte summary must be refused as bad_value: %d %s", code, se)
	}
	if !strings.Contains(se, "300 bytes") {
		t.Fatalf("hint must name the 300-byte bound: %s", se)
	}

	exactly300 := strings.Repeat("x", 300)
	_, se, code = run(t, dir, "rollup", b, "-m", exactly300, "--as", "curator")
	if code != 0 {
		t.Fatalf("300-byte summary must be accepted: %s", se)
	}
}

func TestTailRootsAndDrill(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	b := writeEv(t, dir, "k2")

	// pre-rollup: default tail is byte-identical to the raw view minus the raw flag
	before, _, _ := run(t, dir, "tail", "-n", "50")
	beforeDoc := mustJSON(t, before)
	if _, has := beforeDoc["rollup_due"]; has {
		t.Fatalf("default tail must not grow new fields (byte-identical contract)")
	}
	// F6: with nothing rolled up yet, roots are exactly the raw chain — the
	// default view's "events" must deep-equal the raw view's.
	beforeRaw, _, _ := run(t, dir, "tail", "--raw", "-n", "50")
	beforeRawDoc := mustJSON(t, beforeRaw)
	if !jsonEq(t, beforeDoc["events"], beforeRawDoc["events"]) {
		t.Fatalf("pre-rollup default tail must equal raw tail's events:\ndefault %v\nraw %v",
			beforeDoc["events"], beforeRawDoc["events"])
	}

	so, se, code := run(t, dir, "rollup", a, b, "-m", "k-thread finished", "--as", "curator")
	if code != 0 {
		t.Fatalf("%s", se)
	}
	rid := mustJSON(t, so)["id"].(string)

	// default tail now collapses: no event with id a or b; one rollup root
	so, _, _ = run(t, dir, "tail", "-n", "50")
	if strings.Contains(so, `"id": "`+a+`"`) || strings.Contains(so, `"id": "`+b+`"`) {
		t.Fatalf("encapsulated events leaked into roots view: %s", so)
	}
	if !strings.Contains(so, rid) {
		t.Fatalf("rollup root missing: %s", so)
	}

	// --raw still shows everything
	so, _, _ = run(t, dir, "tail", "--raw", "-n", "50")
	if !strings.Contains(so, `"`+a+`"`) || !strings.Contains(so, `"raw": true`) {
		t.Fatalf("raw view must show the full chain: %s", so)
	}

	// --in opens the rollup
	so, se, code = run(t, dir, "tail", "--in", rid)
	if code != 0 {
		t.Fatalf("--in failed: %s", se)
	}
	doc := mustJSON(t, so)
	if doc["summary"] != "k-thread finished" || len(doc["events"].([]any)) != 2 {
		t.Fatalf("--in wrong: %v", doc)
	}

	// --in on a non-rollup id is unknown_event; --raw + --in is bad_value
	_, se, code = run(t, dir, "tail", "--in", a)
	if code != 4 || !strings.Contains(se, "unknown_event") {
		t.Fatalf("--in non-rollup: %d %s", code, se)
	}
	// F11: the hint must name --in and must not carry the old double space
	if !strings.Contains(se, "ledger tail shows the current roots; a rollup line's id works with --in") {
		t.Fatalf("--in non-rollup hint wrong: %s", se)
	}
	if strings.Contains(se, "tail  shows") {
		t.Fatalf("--in non-rollup hint must not carry the old double space: %s", se)
	}
	_, se, code = run(t, dir, "tail", "--raw", "--in", rid)
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("--raw --in: %d %s", code, se)
	}
}

func TestRollupOnClosedLedgerAllowed(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	if _, se, code := run(t, dir, "close", "demo", "--as-state", "shipped", "--as", "w"); code != 0 {
		t.Fatalf("close: %s", se)
	}
	_, se, code := run(t, dir, "rollup", a, "-m", "post-close curation", "--as", "curator", "--ledger", "demo")
	if code != 0 {
		t.Fatalf("rollup must be legal on a closed ledger (note precedent): %s", se)
	}
}

func TestRollupDueInWriteEnvelopes(t *testing.T) {
	dir := setup(t)
	so, _, _ := run(t, dir, "set", "k1", "status=open", "--as", "w")
	if _, ok := mustJSON(t, so)["rollup_due"]; !ok {
		t.Fatalf("set envelope missing rollup_due: %s", so)
	}
	so, _, _ = run(t, dir, "note", "-k", "gotcha", "-m", "x", "--as", "w")
	if _, ok := mustJSON(t, so)["rollup_due"]; !ok {
		t.Fatalf("note envelope missing rollup_due: %s", so)
	}

	// F10b: create, vocab add, and close envelopes also carry rollup_due.
	so, _, code := run(t, dir, "create", "demo2", "--scope", "second ledger", "--as", "w")
	if code != 0 {
		t.Fatalf("create: %s", so)
	}
	if _, ok := mustJSON(t, so)["rollup_due"]; !ok {
		t.Fatalf("create envelope missing rollup_due: %s", so)
	}

	so, _, code = run(t, dir, "vocab", "add", "demo", "status", "archived", "-m", "why", "--as", "w")
	if code != 0 {
		t.Fatalf("vocab add: %s", so)
	}
	if _, ok := mustJSON(t, so)["rollup_due"]; !ok {
		t.Fatalf("vocab add envelope missing rollup_due: %s", so)
	}

	so, _, code = run(t, dir, "close", "demo2", "--as-state", "abandoned", "--as", "w")
	if code != 0 {
		t.Fatalf("close: %s", so)
	}
	if _, ok := mustJSON(t, so)["rollup_due"]; !ok {
		t.Fatalf("close envelope missing rollup_due: %s", so)
	}
}

func TestStateFoldUnchangedByRollups(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	before, _, _ := run(t, dir, "show")
	beforeStatus, _, _ := run(t, dir, "status")
	if _, se, code := run(t, dir, "rollup", a, "-m", "k1 done", "--as", "c"); code != 0 {
		t.Fatalf("%s", se)
	}
	after, _, _ := run(t, dir, "show")
	afterStatus, _, _ := run(t, dir, "status")
	// spec test 43: byte-identical modulo the event count/head lines, which
	// legitimately advance. Compare rows/spine JSON fields exactly.
	if mustJSON(t, before)["rows"] == nil ||
		!jsonEq(t, mustJSON(t, before)["rows"], mustJSON(t, after)["rows"]) ||
		!jsonEq(t, mustJSON(t, beforeStatus)["rows"], mustJSON(t, afterStatus)["rows"]) {
		t.Fatalf("state fold changed after rollup:\nbefore %s\nafter %s", before, after)
	}
}

// TestSinceDeliversRollup: spec test 43 names `since` explicitly — a rollup
// event must come back like any other event in a since drain, not be
// filtered out the way watch's --key/--value/--kind filters skip it.
func TestSinceDeliversRollup(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	so, _, _ := run(t, dir, "since", "--limit", "1")
	cur := mustJSON(t, so)["cursor"].(string)

	ro, _, code := run(t, dir, "rollup", a, "-m", "done", "--as", "curator")
	if code != 0 {
		t.Fatal(ro)
	}
	rid := mustJSON(t, ro)["id"].(string)

	so, _, code = run(t, dir, "since", cur)
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	found := false
	for _, e := range doc["events"].([]any) {
		m := e.(map[string]any)
		if m["id"] == rid {
			found = true
			if m["type"] != "rollup" {
				t.Fatalf("since's rollup event has wrong type: %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("since must deliver the rollup event %s: %s", rid, so)
	}
}

func TestWatchDeliversRollupsUnfiltered(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	so, _, _ := run(t, dir, "set", "k2", "status=open", "--as", "w")
	cur := mustJSON(t, so)["id"].(string)
	ro, _, _ := run(t, dir, "rollup", a, "-m", "done", "--as", "c")
	rid := mustJSON(t, ro)["id"].(string)

	so, _, code := run(t, dir, "watch", "--since", cur, "--timeout", "1")
	if code != 0 {
		t.Fatalf("watch should drain the rollup event, got exit %d: %s", code, so)
	}
	if !strings.Contains(so, rid) {
		t.Fatalf("unfiltered watch must deliver the rollup: %s", so)
	}
	// filtered watch skips rollups but its cursor advances past them
	so, _, code = run(t, dir, "watch", "--since", cur, "--key", "nothing-matches", "--timeout", "1")
	if code != 2 {
		t.Fatalf("filtered watch: want timeout exit 2, got %d: %s", code, so)
	}
	if !strings.Contains(so, `"cursor"`) {
		t.Fatalf("timeout must still carry a cursor: %s", so)
	}
}

// TestRollupIdempotencyKey mirrors set/note's --idempotency-key dedupe: a
// rollup write is a no-op iff a prior ROLLUP event by the same author
// carries the same idempotency key. Rollups have no item key, so the scope
// is just (author, key) — the pair is the whole scope.
func TestRollupIdempotencyKey(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	b := writeEv(t, dir, "k2")

	so1, _, code := run(t, dir, "rollup", a, "-m", "first", "--as", "curator", "--idempotency-key", "r1")
	if code != 0 {
		t.Fatal(so1)
	}
	d1 := mustJSON(t, so1)

	// same author + same key -> deduped, with causing sha+author
	so2, _, code := run(t, dir, "rollup", b, "-m", "second", "--as", "curator", "--idempotency-key", "r1")
	if code != 0 {
		t.Fatal(so2)
	}
	d2 := mustJSON(t, so2)
	if d2["deduped"] != true || d2["id"] != d1["id"] || d2["by"] != "curator" {
		t.Fatalf("same author+key must dedupe against the causing sha+author: %v", d2)
	}
	// F7: a deduped early return still carries rollup_due, same as a normal write
	if _, ok := d2["rollup_due"]; !ok {
		t.Fatalf("deduped rollup envelope missing rollup_due: %v", d2)
	}

	// the children named in the deduped call must NOT be claimed by it — b
	// is still available for a real rollup
	so3, _, code := run(t, dir, "rollup", b, "-m", "actually roll b", "--as", "curator2")
	if code != 0 {
		t.Fatalf("child b must not have been claimed by the deduped rollup: %s", so3)
	}

	// different author, same key -> lands (author-scoped, not just key-scoped)
	cID := writeEv(t, dir, "k3")
	so4, _, code := run(t, dir, "rollup", cID, "-m", "third", "--as", "other", "--idempotency-key", "r1")
	if code != 0 {
		t.Fatal(so4)
	}
	if mustJSON(t, so4)["deduped"] == true {
		t.Fatal("different author, same key must NOT dedupe (spec: author-scoped)")
	}
}

// TestRollupDuplicateIdsInArgvDeduped: `rollup <a> <a> -m ...` must collapse
// the repeated id to a single child, both in the envelope and in the event
// actually stored (checked via tail --raw, not just the response payload).
func TestRollupDuplicateIdsInArgvDeduped(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	so, _, code := run(t, dir, "rollup", a, a, "-m", "dup ids in argv", "--as", "curator")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	if doc["children"].(float64) != 1 {
		t.Fatalf("duplicate ids in argv must collapse to one child in the envelope: %v", doc)
	}
	rid := doc["id"].(string)

	rawSO, _, _ := run(t, dir, "tail", "--raw", "-n", "50")
	rawDoc := mustJSON(t, rawSO)
	found := false
	for _, e := range rawDoc["events"].([]any) {
		m := e.(map[string]any)
		if m["id"] != rid {
			continue
		}
		found = true
		children, _ := m["children"].([]any)
		if len(children) != 1 {
			t.Fatalf("stored rollup event must have exactly one child, got %v: %v", children, m)
		}
	}
	if !found {
		t.Fatalf("rollup event %s not found in raw chain: %s", rid, rawSO)
	}
}

// TestConcurrentRollupDuelAllOrNothing races two rollup writes that both
// claim child a (one also claims b), appending through store.Store directly
// to bypass the CLI's write-time child_taken check — the only way to force
// the duel CAS is meant to resolve. Proves: both appends land (CAS
// serializes, neither is lost), fold's total order picks exactly one
// winner, the loser is wholly inert (all-or-nothing: it doesn't keep the
// child the winner never claimed), and tail --raw flags the loser while
// tail's roots view excludes it. Assertions are outcome-symmetric: they
// hold whichever racer lands first in the chain (see fold.Fold's duel
// resolution: whoever is processed first in event order wins).
func TestConcurrentRollupDuelAllOrNothing(t *testing.T) {
	dir := setup(t)
	a := writeEv(t, dir, "k1")
	b := writeEv(t, dir, "k2")

	st := store.Store{Repo: gitx.Repo{Dir: dir}}
	var wg sync.WaitGroup
	for i, kids := range [][]string{{a}, {a, b}} {
		wg.Add(1)
		go func(n int, children []string) {
			defer wg.Done()
			ev := model.NewEvent("rollup", "racer-"+strconv.Itoa(n), st.Repo)
			ev.Children = children
			ev.Text = "race " + strconv.Itoa(n)
			if _, err := st.Append("demo", ev, nil, store.ExpectPresent); err != nil {
				t.Errorf("append %d: %v", n, err)
			}
		}(i, kids)
	}
	wg.Wait()

	evs, meta, err := st.Events("demo")
	if err != nil {
		t.Fatal(err)
	}
	led := fold.Fold("demo", evs, meta)
	var rollups []model.Event
	for _, e := range evs {
		if e.Type == "rollup" {
			rollups = append(rollups, e)
		}
	}
	if len(rollups) != 2 {
		t.Fatalf("both racing appends must land (CAS serializes): got %d", len(rollups))
	}
	winner, loser := rollups[0], rollups[1] // total order: first in chain wins
	if !led.Losers[loser.ID] || led.Losers[winner.ID] {
		t.Fatalf("first-in-total-order must win: losers=%v", led.Losers)
	}
	if led.Parent[a] != winner.ID {
		t.Fatalf("child a owned by %s, want %s", led.Parent[a], winner.ID)
	}
	if _, taken := led.Parent[b]; taken && led.Losers[loser.ID] && model.Contains(loser.Children, b) {
		t.Fatalf("all-or-nothing: loser must not keep b")
	}

	// the loser is visible and flagged in tail --raw
	rawSO, _, _ := run(t, dir, "tail", "--raw", "-n", "50")
	if !strings.Contains(rawSO, `"duel_loser": true`) {
		t.Fatalf("raw view must flag the duel loser: %s", rawSO)
	}
	rawDoc := mustJSON(t, rawSO)
	loserFlagged := false
	for _, e := range rawDoc["events"].([]any) {
		m := e.(map[string]any)
		if m["id"] == loser.ID {
			loserFlagged = m["duel_loser"] == true
		}
	}
	if !loserFlagged {
		t.Fatalf("raw view's duel_loser flag must land on %s specifically: %s", loser.ID, rawSO)
	}

	// and absent from roots. Note: the payload's top-level "cursor" field is
	// always the raw chain tip (led.Head()) for pagination, independent of
	// --raw — since the loser is the last event appended here, it legitimately
	// appears there. So check the "events" (roots) list specifically, not the
	// whole payload string.
	so, _, _ := run(t, dir, "tail", "-n", "50")
	doc := mustJSON(t, so)
	for _, e := range doc["events"].([]any) {
		if e.(map[string]any)["id"] == loser.ID {
			t.Fatalf("loser must not be a root: %s", so)
		}
	}
}
