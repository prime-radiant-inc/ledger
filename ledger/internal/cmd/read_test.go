package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/store"
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
	if doc["ledger"] != "demo" {
		t.Fatalf("notes payload must carry ledger like every other verb: %v", doc)
	}
}

func TestTailCursor(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "tail", "-n", "3")
	doc := mustJSON(t, so)
	if len(doc["events"].([]any)) != 3 || doc["cursor"] == nil {
		t.Fatalf("tail: %v", doc)
	}
	if doc["ledger"] != "demo" {
		t.Fatalf("tail payload must carry ledger like every other verb: %v", doc)
	}
}

// TestShowTTYNoteSummaryOneLine: show's TTY notes section is a compact
// summary (first line only, truncated/escaped) — a multi-line body must
// never spill onto the terminal the way notes/status drill-down's full
// render does. Bypasses run() (which never sets TTY) to invoke runShow
// directly with an explicit TTY Ctx.
func TestShowTTYNoteSummaryOneLine(t *testing.T) {
	dir := seed(t)
	run(t, dir, "note", "-k", "handoff", "-m", "line one\nline two\nline three", "--as", "x")

	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}
	if err := runShow(c, ""); err != nil {
		t.Fatal(err)
	}
	rendered := buf.String()
	if strings.Contains(rendered, "line two") || strings.Contains(rendered, "line three") {
		t.Fatalf("show TTY must summarize a note to its first line, not print the full body: %q", rendered)
	}
	if !strings.Contains(rendered, "line one") {
		t.Fatalf("show TTY must still show the first line: %q", rendered)
	}
}

// TestSyncEventsExcludedFromTailAndShowCount: sync events are invisible to
// fold's schema/spine/state already; tail and show's event count must keep
// that invisibility rather than leaking merge plumbing into a render.
func TestSyncEventsExcludedFromTailAndShowCount(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "show")
	before := mustJSON(t, so)["events"].(float64)

	st := store.Store{Repo: gitx.Repo{Dir: dir}}
	syncEv := model.Event{TS: "2026-08-13T00:00:00", Type: "sync", Author: "sync-bot"}
	if _, err := st.Append("demo", syncEv, nil, store.ExpectPresent); err != nil {
		t.Fatal(err)
	}

	so2, _, _ := run(t, dir, "show")
	after := mustJSON(t, so2)["events"].(float64)
	if after != before {
		t.Fatalf("show's events count must exclude sync events: before=%v after=%v", before, after)
	}

	tailSo, _, _ := run(t, dir, "tail", "-n", "1")
	events := mustJSON(t, tailSo)["events"].([]any)
	last := events[len(events)-1].(map[string]any)
	if last["type"] == "sync" {
		t.Fatalf("tail must exclude sync events: %v", events)
	}
}

// TestStatusTTYEscapesControlChars: an item key carrying a raw \r is exactly
// the counterfeit-provenance vector spineLine's escaping already closed for
// the note-body column — the same rule must cover the key column too.
// Bypasses run() (never TTY) for a direct TTY Ctx, same pattern as
// TestShowTTYNoteSummaryOneLine.
func TestStatusTTYEscapesControlChars(t *testing.T) {
	dir := setup(t)
	run(t, dir, "set", "t1\rFORGED", "open", "--as", "impl")

	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}
	if err := runStatus(c, "", "", false, ""); err != nil {
		t.Fatal(err)
	}
	rendered := buf.String()
	if strings.Contains(rendered, "\r") {
		t.Fatalf("status TTY must escape control chars in the key column: %q", rendered)
	}
	if !strings.Contains(rendered, "^M") {
		t.Fatalf("expected the escaped \\r as ^M: %q", rendered)
	}
}

// TestStatusNotesTailCarrySupersededRedirect: status (both the spine listing
// and a key drill-down), notes, and tail all read led.SupersededBy the same
// way show already did — every read verb, not just show, must redirect a
// caller off a superseded ledger.
func TestStatusNotesTailCarrySupersededRedirect(t *testing.T) {
	dir := seed(t)
	run(t, dir, "create", "demo2", "--scope", "next", "--supersedes", "demo")

	so, _, _ := run(t, dir, "status", "--ledger", "demo")
	if mustJSON(t, so)["superseded_by"] != "demo2" {
		t.Fatalf("status must carry the redirect: %s", so)
	}
	so, _, _ = run(t, dir, "status", "t1", "--ledger", "demo")
	if mustJSON(t, so)["superseded_by"] != "demo2" {
		t.Fatalf("status drill-down must carry the redirect: %s", so)
	}
	so, _, _ = run(t, dir, "notes", "--ledger", "demo")
	if mustJSON(t, so)["superseded_by"] != "demo2" {
		t.Fatalf("notes must carry the redirect: %s", so)
	}
	so, _, _ = run(t, dir, "tail", "--ledger", "demo")
	if mustJSON(t, so)["superseded_by"] != "demo2" {
		t.Fatalf("tail must carry the redirect: %s", so)
	}
}

// TestRenderWritesDeterministicFile: render --to writes show's projection to
// a file, and — unlike show's TTY render, which timestamps notes with a
// wall-clock-relative Age() — must be byte-identical across two runs against
// the same, unchanged ledger state.
func TestRenderWritesDeterministicFile(t *testing.T) {
	dir := seed(t)
	outPath := filepath.Join(t.TempDir(), "out.txt")

	so, _, code := run(t, dir, "render", "--to", outPath)
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	if doc["ledger"] != "demo" || doc["path"] != outPath || doc["bytes"] == nil {
		t.Fatalf("render payload: %v", doc)
	}
	b1, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b1), "t1") {
		t.Fatalf("rendered file must contain the spine row: %s", b1)
	}

	if _, _, code := run(t, dir, "render", "--to", outPath); code != 0 {
		t.Fatal("second render must also succeed")
	}
	b2, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("render must be byte-identical across runs with no new events:\n%s\n---\n%s", b1, b2)
	}
}
