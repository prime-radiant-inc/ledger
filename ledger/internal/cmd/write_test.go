package cmd

import (
	"strings"
	"testing"
)

func setup(t *testing.T) string {
	dir := initRepo(t)
	run(t, dir, "create", "demo", "--scope", "test",
		"--field", "status=open,done,failed", "--field", "review=pending,approved",
		"--require-evidence", "status=done")
	return dir
}

func TestSetBareAndMultiField(t *testing.T) {
	dir := setup(t)
	so, _, code := run(t, dir, "set", "t1", "open", "review=pending", "--as", "impl")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	f := doc["fields"].(map[string]any)
	if f["status"] != "open" || f["review"] != "pending" {
		t.Fatalf("bare value must hit first declared field: %v", f)
	}
}

func TestSetRejections(t *testing.T) {
	dir := setup(t)
	_, se, code := run(t, dir, "set", "t1", "done", "--as", "impl")
	if code != 4 || !strings.Contains(se, "evidence_required") {
		t.Fatalf("%s", se)
	}
	so, _, code := run(t, dir, "set", "t1", "done", "--evidence", "commit:abc123", "--as", "impl")
	if code != 0 {
		t.Fatal(so)
	}
	_, se, _ = run(t, dir, "set", "t1", "wat", "--as", "impl")
	if !strings.Contains(se, "vocab_unknown") || !strings.Contains(se, "ledger vocab add demo status wat") {
		t.Fatalf("hint must be the exact command: %s", se)
	}
	_, se, _ = run(t, dir, "set", "t1", "severity=high", "--as", "impl")
	if !strings.Contains(se, "unknown_field") || !strings.Contains(se, "status") {
		t.Fatalf("%s", se)
	}
	_, se, _ = run(t, dir, "set", "t1", "--as")
	_ = se // cobra arg error; just must not panic or write
}

func TestIdempotencyAuthorScoped(t *testing.T) {
	dir := setup(t)
	so1, _, _ := run(t, dir, "set", "t1", "open", "--as", "a", "--idempotency-key", "t1-open")
	so2, _, _ := run(t, dir, "set", "t1", "open", "--as", "a", "--idempotency-key", "t1-open")
	d1, d2 := mustJSON(t, so1), mustJSON(t, so2)
	if d2["deduped"] != true || d2["id"] != d1["id"] {
		t.Fatalf("same author+key must dedupe: %v", d2)
	}
	so3, _, _ := run(t, dir, "set", "t1", "failed", "--as", "b", "--idempotency-key", "t1-open")
	if mustJSON(t, so3)["deduped"] == true {
		t.Fatal("different author must NOT dedupe (spec: author-scoped)")
	}
}

func TestNoteBodySources(t *testing.T) {
	dir := setup(t)
	_, se, code := run(t, dir, "note", "-k", "handoff", "-m", "short", "--from-file", "/dev/null")
	if code != 4 || !strings.Contains(se, "conflicting_body") {
		t.Fatalf("%s", se)
	}
	so, _, code := run(t, dir, "note", "-k", "gotcha", "--key", "t1", "-m", "trap here", "--as", "x")
	if code != 0 || mustJSON(t, so)["kind"] != "gotcha" {
		t.Fatal(so)
	}
	_, se, code = run(t, dir, "note", "-k", "x", "-m", "  ")
	if code != 4 || !strings.Contains(se, "empty_body") {
		t.Fatalf("%s", se)
	}
}

func TestClosedLedgerRules(t *testing.T) {
	dir := setup(t)
	run(t, dir, "close", "demo", "--as-state", "abandoned")
	_, se, code := run(t, dir, "set", "t1", "open", "--ledger", "demo")
	if code != 4 || !strings.Contains(se, "closed") {
		t.Fatalf("%s", se)
	}
	_, _, code = run(t, dir, "note", "-k", "postmortem", "-m", "lessons", "--ledger", "demo")
	if code != 0 {
		t.Fatal("closed ledgers accept notes")
	}
}
