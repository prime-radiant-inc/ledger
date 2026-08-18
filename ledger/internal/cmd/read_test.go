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
	"ledger/internal/out"
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
	if err := runShow(c, "", nil, ""); err != nil {
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
// the note-body column — the same rule must cover the key column, and the
// evidence column (--evidence is just as user-controlled as a note body),
// too. Bypasses run() (never TTY) for a direct TTY Ctx, same pattern as
// TestShowTTYNoteSummaryOneLine.
func TestStatusTTYEscapesControlChars(t *testing.T) {
	dir := setup(t)
	run(t, dir, "set", "t1\rFORGED", "open", "--as", "impl")
	run(t, dir, "set", "t2", "done", "--evidence", "commit:abc\rFORGED", "--as", "impl")

	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}
	if err := runStatus(c, "", "", false, ""); err != nil {
		t.Fatal(err)
	}
	rendered := buf.String()
	if strings.Contains(rendered, "\r") {
		t.Fatalf("status TTY must escape control chars in the key and evidence columns: %q", rendered)
	}
	if !strings.Contains(rendered, "^M") {
		t.Fatalf("expected the escaped \\r as ^M: %q", rendered)
	}
	if !strings.Contains(rendered, "commit:abc^MFORGED") {
		t.Fatalf("evidence column must carry the escaped, not raw, ref: %q", rendered)
	}
}

// TestStatusNotesTailCarrySupersededRedirect: status (both the spine listing
// and a key drill-down), notes, tail, and show --id all read
// led.SupersededBy the same way show already did — every read verb, not
// just bare show, must redirect a caller off a superseded ledger.
func TestStatusNotesTailCarrySupersededRedirect(t *testing.T) {
	dir := seed(t)
	id := rawEventID(t, dir, func(ev map[string]any) bool { return ev["type"] == "set" })
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
	so, _, _ = run(t, dir, "show", "--id", id, "--ledger", "demo")
	if mustJSON(t, so)["superseded_by"] != "demo2" {
		t.Fatalf("show --id must carry the redirect: %s", so)
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

// rawEventID finds the id of the first raw event matching pred — the id
// reads tests need a real event/note id off the chain, not a guessed one.
func rawEventID(t *testing.T, dir string, pred func(ev map[string]any) bool) string {
	t.Helper()
	tso, _, _ := run(t, dir, "tail", "--raw", "-n", "100")
	for _, e := range mustJSON(t, tso)["events"].([]any) {
		ev := e.(map[string]any)
		if pred(ev) {
			return ev["id"].(string)
		}
	}
	t.Fatal("no matching raw event found")
	return ""
}

// TestShowIDRendersSetEvent: show --id on a set-event id renders the event
// in full (sync spec Addition 3, "Id reads, both paths pinned") — type,
// key, author, and provenance must all be present in the JSON payload.
func TestShowIDRendersSetEvent(t *testing.T) {
	dir := seed(t)
	id := rawEventID(t, dir, func(ev map[string]any) bool {
		return ev["type"] == "set" && ev["key"] == "t2"
	})
	so, _, code := run(t, dir, "show", "--id", id)
	if code != 0 {
		t.Fatalf("show --id: %s", so)
	}
	doc := mustJSON(t, so)
	if doc["type"] != "set" || doc["key"] != "t2" || doc["author"] != "reviewer" {
		t.Fatalf("show --id must render the full event: %v", doc)
	}
	if doc["via"] == nil || doc["via"] == "" {
		t.Fatalf("show --id must render author provenance: %v", doc)
	}
	if doc["ledger"] != "demo" {
		t.Fatalf("show --id payload must carry ledger like every other verb: %v", doc)
	}
}

// TestShowIDRendersNoteBody: show --id on a note id renders the body under
// the parent's per-line quoting and control-character escaping — the same
// treatment noteLines gives a note body, reused rather than reimplemented.
func TestShowIDRendersNoteBody(t *testing.T) {
	dir := seed(t)
	run(t, dir, "note", "-k", "handoff", "-m", "line one\nbad\rFORGED", "--as", "x")
	id := rawEventID(t, dir, func(ev map[string]any) bool {
		return ev["type"] == "note" && ev["author"] == "x"
	})

	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: true, Stdout: &buf, Stderr: &buf}
	if err := runShow(c, "", nil, id); err != nil {
		t.Fatal(err)
	}
	rendered := buf.String()
	if !strings.Contains(rendered, "  | line one") {
		t.Fatalf("show --id must render the note body, per-line quoted: %q", rendered)
	}
	if strings.Contains(rendered, "\r") {
		t.Fatalf("show --id must control-escape the body, not print it raw: %q", rendered)
	}
	if !strings.Contains(rendered, "^M") {
		t.Fatalf("expected the escaped \\r as ^M: %q", rendered)
	}

	so, _, code := run(t, dir, "show", "--id", id)
	if code != 0 {
		t.Fatalf("show --id: %s", so)
	}
	doc := mustJSON(t, so)
	if doc["type"] != "note" || doc["kind"] != "handoff" {
		t.Fatalf("show --id on a note must render its type/kind: %v", doc)
	}
	if !strings.Contains(doc["text"].(string), "\r") {
		t.Fatal("JSON must carry the raw, unescaped body")
	}
}

// TestShowIDUnknown: an unknown id is bad_value naming it (never a bare
// "not found" without the offending id, and never anything but exit 4).
func TestShowIDUnknown(t *testing.T) {
	dir := seed(t)
	_, se, code := run(t, dir, "show", "--id", "0000000000")
	if code != 4 || !strings.Contains(se, "bad_value") || !strings.Contains(se, "0000000000") {
		t.Fatalf("unknown id must be bad_value naming it: code=%d se=%s", code, se)
	}
}

// TestFindByIDAmbiguousPrefix: ids are 10-hex truncated shas, so a short
// --id prefix genuinely can match more than one event (not a hypothetical —
// git abbreviation collisions are exactly this shape). show --id and
// notes --id must both refuse to guess.
func TestFindByIDAmbiguousPrefix(t *testing.T) {
	evs := []model.Event{{ID: "aaaa000001", Type: "set"}, {ID: "aaaa000002", Type: "note"}}
	if _, matches := findByID(evs, "aaaa"); matches != 2 {
		t.Fatalf("expected 2 matches on the shared prefix, got %d", matches)
	}

	showErr, ok := idReadErr("demo", "aaaa", 2).(*out.CLIError)
	if !ok || showErr.Code != "bad_value" || !strings.Contains(showErr.Message, "ambiguous") {
		t.Fatalf("show --id's ambiguous-prefix error must be bad_value naming the ambiguity: %+v", showErr)
	}

	notesErr, ok := notesIDErr("demo", "aaaa", model.Event{}, 2).(*out.CLIError)
	if !ok || notesErr.Code != "bad_value" || !strings.Contains(notesErr.Message, "ambiguous") {
		t.Fatalf("notes --id's ambiguous-prefix error must be bad_value naming the ambiguity: %+v", notesErr)
	}
	if !strings.Contains(notesErr.Hint, "show --id") {
		t.Fatalf("notes --id's ambiguous-prefix error must still hint at show --id: %+v", notesErr)
	}
}

// TestNotesIDOnSetEventErrorsWithShowHint: notes --id on a real event id
// that is NOT a note — a set event's id — must never fall through to a
// silent empty list; it errors bad_value naming the id, hinting at
// `show --id` (the parent's "empty results announce themselves" rule).
func TestNotesIDOnSetEventErrorsWithShowHint(t *testing.T) {
	dir := seed(t)
	id := rawEventID(t, dir, func(ev map[string]any) bool { return ev["type"] == "set" })
	_, se, code := run(t, dir, "notes", "--id", id)
	if code != 4 || !strings.Contains(se, "bad_value") {
		t.Fatalf("notes --id on a non-note event must be bad_value: code=%d se=%s", code, se)
	}
	if !strings.Contains(se, id) {
		t.Fatalf("error must name the id: %s", se)
	}
	if !strings.Contains(se, "show --id") {
		t.Fatalf("hint must point at show --id: %s", se)
	}
}

// TestNotesIDUnknownErrorsWithShowHint: an id that matches no event at all
// gets the identical bad_value/show --id treatment as a non-note event —
// the spec's "non-note event and unknown id alike" clause.
func TestNotesIDUnknownErrorsWithShowHint(t *testing.T) {
	dir := seed(t)
	_, se, code := run(t, dir, "notes", "--id", "0000000000")
	if code != 4 || !strings.Contains(se, "bad_value") || !strings.Contains(se, "show --id") {
		t.Fatalf("unknown id on notes --id must be bad_value with a show --id hint: code=%d se=%s", code, se)
	}
}

// TestNotesIDOnRealNoteUnchanged: notes --id keeps its existing
// note-rendering contract for an id that actually is a note.
func TestNotesIDOnRealNoteUnchanged(t *testing.T) {
	dir := seed(t)
	id := rawEventID(t, dir, func(ev map[string]any) bool {
		return ev["type"] == "note" && ev["kind"] == "handoff"
	})
	so, _, code := run(t, dir, "notes", "--id", id)
	if code != 0 {
		t.Fatalf("notes --id on a real note must succeed: %s", so)
	}
	notes := mustJSON(t, so)["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("notes --id on a real note id must return exactly it: %v", notes)
	}
}
