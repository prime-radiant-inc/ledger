package cmd

import (
	"strings"
	"testing"
)

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
