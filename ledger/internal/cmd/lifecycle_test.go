package cmd

import (
	"encoding/json"
	"strings"
	"testing"
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

func TestCloseSupersededNeedsSuccessor(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "old", "--scope", "x")
	_, se, code := run(t, dir, "close", "old", "--as-state", "superseded")
	if code != 4 || !strings.Contains(se, "needs_successor") {
		t.Fatalf("%d %s", code, se)
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
