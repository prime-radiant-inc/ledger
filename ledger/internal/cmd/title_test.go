package cmd

import (
	"strings"
	"testing"
)

// TestEmptyBodyOnFirstStatusWrite: on a ready-capable board, the first
// status write on a key requires a non-empty, trimmed -m — spec "Titles":
// the first status write's message becomes the key's title, so a missing
// or whitespace-only -m is empty_body, exit 4, with the pinned hint.
func TestEmptyBodyOnFirstStatusWrite(t *testing.T) {
	dir := setupReady(t)

	_, se, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "--as", "a")
	if code != 4 {
		t.Fatalf("missing -m on first status write must fail: %d %s", code, se)
	}
	doc := mustJSON(t, se)
	if doc["error"] != "empty_body" {
		t.Fatalf("%s", se)
	}
	wantHint := "the first status write's -m becomes the key's title"
	if doc["hint"] != wantHint {
		t.Fatalf("exact hint: got %q want %q", doc["hint"], wantHint)
	}

	_, se2, code2 := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "   ", "--as", "a")
	if code2 != 4 || mustJSON(t, se2)["error"] != "empty_body" {
		t.Fatalf("whitespace-only -m must also be empty_body: %d %s", code2, se2)
	}
}

// TestTitleFromFirstStatusWriteSurvivesLifecycle: the title is fixed at the
// first status write's -m and is unaffected by later claim/close/revision
// writes on the same key — it shows up in `show` rows on a ready-capable
// board.
func TestTitleFromFirstStatusWriteSurvivesLifecycle(t *testing.T) {
	dir := setupReady(t)

	so, _, code := run(t, dir, "set", "k1", "status=open", "--expect", "none", "-m", "Fix the flaky retry", "--as", "a")
	if code != 0 {
		t.Fatal(so)
	}
	seedID := mustJSON(t, so)["id"].(string)

	so, _, code = run(t, dir, "show")
	if code != 0 {
		t.Fatal(so)
	}
	if title := titleFor(t, so, "k1"); title != "Fix the flaky retry" {
		t.Fatalf("title after seed: got %q", title)
	}

	so2, _, code2 := run(t, dir, "set", "k1", "status=in-progress", "--expect", seedID, "-m", "claiming, not a title", "--as", "b")
	if code2 != 0 {
		t.Fatal(so2)
	}
	claimID := mustJSON(t, so2)["id"].(string)

	so, _, _ = run(t, dir, "show")
	if title := titleFor(t, so, "k1"); title != "Fix the flaky retry" {
		t.Fatalf("title must survive claim: got %q", title)
	}

	_, _, code3 := run(t, dir, "set", "k1", "status=closed", "--expect", claimID, "-m", "closing, not a title", "--as", "b")
	if code3 != 0 {
		t.Fatal("close must succeed")
	}

	so, _, _ = run(t, dir, "show")
	if title := titleFor(t, so, "k1"); title != "Fix the flaky retry" {
		t.Fatalf("title must survive close: got %q", title)
	}
}

// TestPlainBoardBareStatusSetStillWorksNoMessage: parent semantics are
// untouched on a plain board — a bare status set with no -m still works,
// since empty_body is ready-capable-only.
func TestPlainBoardBareStatusSetStillWorksNoMessage(t *testing.T) {
	dir := setup(t) // plain board, from write_test.go
	so, se, code := run(t, dir, "set", "t1", "open", "--as", "impl")
	if code != 0 {
		t.Fatalf("bare status set with no -m must still work on a plain board: %s %s", so, se)
	}
}

// titleFor extracts the "title" field of the row for key on show's JSON
// payload (any field's row carries it once the key has a title).
func titleFor(t *testing.T, showJSON, key string) string {
	t.Helper()
	doc := mustJSON(t, showJSON)
	rows := doc["rows"].([]any)
	for _, r := range rows {
		m := r.(map[string]any)
		if m["key"] == key {
			if title, ok := m["title"]; ok {
				return title.(string)
			}
		}
	}
	return ""
}

// TestShowTitleAbsentOnStatuslessAndPlainBoards: a statusless key (no
// status event yet) carries no title in `show` rows, and plain boards never
// carry a title key at all — the JSON tag is omitempty, so both look like
// a missing key.
func TestShowTitleAbsentOnStatuslessAndPlainBoards(t *testing.T) {
	dir := setupReady(t)
	run(t, dir, "set", "k1", "labels=urgent", "--as", "a")
	so, _, code := run(t, dir, "show")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	rows := doc["rows"].([]any)
	found := false
	for _, r := range rows {
		m := r.(map[string]any)
		if m["key"] == "k1" {
			found = true
			if _, ok := m["title"]; ok {
				t.Fatalf("statusless key must not carry a title: %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("expected a row for k1: %s", so)
	}

	dir2 := setup(t) // plain board
	run(t, dir2, "set", "t1", "open", "--as", "a")
	so2, _, _ := run(t, dir2, "show")
	if strings.Contains(so2, `"title"`) {
		t.Fatalf("plain board show rows must never carry a title key: %s", so2)
	}
}
