package cmd

import (
	"encoding/json"
	"strings"
	"testing"
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
	if !strings.Contains(so, "rollup") { // grammar/instructions present in payload
		t.Fatalf("no instructions: %s", so)
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

	// unknown_event
	_, se, code = run(t, dir, "rollup", "deadbeef00", "-m", "x", "--as", "curator")
	if code != 4 || !strings.Contains(se, "unknown_event") {
		t.Fatalf("unknown_event: %d %s", code, se)
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
