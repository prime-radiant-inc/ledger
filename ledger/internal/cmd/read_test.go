package cmd

import (
	"os/exec"
	"strings"
	"testing"
)

func seed(t *testing.T) string {
	dir := setup(t) // from write_test.go: demo with status/review fields
	run(t, dir, "set", "t1", "open", "--as", "impl", "-m", "starting")
	run(t, dir, "set", "t1", "done", "--evidence", "commit:abc123", "--as", "impl", "-m", "finished")
	run(t, dir, "set", "t2", "review=pending", "--as", "reviewer")
	run(t, dir, "note", "-k", "ruling", "--key", "t2", "-m", "ship it", "--as", "jesse")
	run(t, dir, "note", "-k", "handoff", "-m", "resume at t2", "--as", "impl")
	return dir
}

func TestStatusSpine(t *testing.T) {
	dir := seed(t)
	so, _, code := run(t, dir, "status")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	rows := doc["rows"].([]any)
	if len(rows) != 2 { // t1/status latest=done, t2/review
		t.Fatalf("rows: %v", rows)
	}
	r0 := rows[0].(map[string]any)
	if r0["key"] != "t1" || r0["value"] != "done" || r0["note"] != "finished" {
		t.Fatalf("latest-per-(key,field) with -m annotation: %v", r0)
	}
}

func TestStatusDrilldownAndUnknownKey(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "status", "t2")
	doc := mustJSON(t, so)
	if doc["key"] != "t2" || len(doc["notes"].([]any)) != 1 {
		t.Fatalf("drill-down must include attached notes: %v", doc)
	}
	_, se, code := run(t, dir, "status", "nope")
	if code != 4 || !strings.Contains(se, "unknown_key") || !strings.Contains(se, "t1") {
		t.Fatalf("unknown key hint lists known keys: %s", se)
	}
}

func TestByBranchFold(t *testing.T) {
	dir := seed(t)
	wt := t.TempDir()
	exec.Command("git", "-C", dir, "worktree", "add", "-b", "feat", wt).Run()
	run(t, wt, "set", "t2", "review=approved", "--as", "wt-reviewer")
	so, _, _ := run(t, dir, "status", "--by-branch", "--field", "review")
	doc := mustJSON(t, so)
	rows := doc["rows"].([]any)
	if len(rows) != 2 { // pending on main, approved on feat — both visible
		t.Fatalf("by-branch rows: %v", rows)
	}
}

func TestShowSchemaAndRedirect(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "show")
	doc := mustJSON(t, so)
	if doc["schema"] == nil || doc["require_evidence"] == nil || doc["head"] == nil {
		t.Fatalf("show payload: %v", doc)
	}
	if int(doc["events"].(float64)) < 6 {
		t.Fatalf("events count: %v", doc["events"])
	}
	run(t, dir, "create", "demo2", "--scope", "next", "--supersedes", "demo")
	so, _, _ = run(t, dir, "show", "--ledger", "demo")
	if mustJSON(t, so)["superseded_by"] != "demo2" {
		t.Fatal("superseded read must carry the redirect")
	}
}

func TestNotesFiltersAndEscaping(t *testing.T) {
	dir := seed(t)
	run(t, dir, "note", "-k", "gotcha", "-m", "bad\rFORGED line", "--as", "x")
	so, _, _ := run(t, dir, "notes", "-k", "gotcha", "--latest")
	doc := mustJSON(t, so)
	notes := doc["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("latest: %v", notes)
	}
	// raw body is preserved in JSON; escaping is a TTY-render concern
	if !strings.Contains(notes[0].(map[string]any)["text"].(string), "\r") {
		t.Fatal("JSON must carry the raw body")
	}
}

func TestTailCursor(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "tail", "-n", "3")
	doc := mustJSON(t, so)
	if len(doc["events"].([]any)) != 3 || doc["cursor"] == nil {
		t.Fatalf("tail: %v", doc)
	}
}
