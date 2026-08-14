package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/store"
)

func TestSincePagingAndReset(t *testing.T) {
	dir := seed(t) // 7+ events
	so, _, _ := run(t, dir, "since", "--limit", "2")
	doc := mustJSON(t, so)
	if int(doc["count"].(float64)) != 2 {
		t.Fatalf("limit: %v", doc)
	}
	cur := doc["cursor"].(string)
	so, _, _ = run(t, dir, "since", cur)
	doc2 := mustJSON(t, so)
	if int(doc2["count"].(float64)) < 1 {
		t.Fatal("paging must resume after cursor")
	}
	_, se, code := run(t, dir, "since", "ffffffffff")
	if code != 4 || !strings.Contains(se, "reset_required") {
		t.Fatalf("%s", se)
	}
}

// TestSinceResetHintDropsRedrainClause: the old hint told the caller a
// cursorless `since` "re-drains from the start", which isn't true of
// since (it has no state) and conflicts with quickstart rule 6's recovery
// advice (status + tail, never a full re-drain).
func TestSinceResetHintDropsRedrainClause(t *testing.T) {
	dir := seed(t)
	_, se, code := run(t, dir, "since", "ffffffffff")
	if code != 4 || !strings.Contains(se, "reset_required") {
		t.Fatalf("%s", se)
	}
	if strings.Contains(se, "re-drains from the start") {
		t.Fatalf("hint must drop the re-drains clause: %s", se)
	}
	if !strings.Contains(se, "ledger tail -n 50 shows recent events") {
		t.Fatalf("hint must point at tail -n 50: %s", se)
	}
}

func TestWatchDrainAndTimeout(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "tail", "-n", "1")
	first := mustJSON(t, so)["events"].([]any)[0].(map[string]any)["id"].(string)
	_ = first
	// drain: watch from the very first event id
	so, _, _ = run(t, dir, "since", "--limit", "1")
	c0 := mustJSON(t, so)["cursor"].(string)
	so, _, code := run(t, dir, "watch", "--since", c0, "--timeout", "5")
	if code != 0 {
		t.Fatalf("drain should match existing sets: %s", so)
	}
	doc := mustJSON(t, so)
	if doc["cursor"] == nil || len(doc["events"].([]any)) == 0 {
		t.Fatalf("watch payload: %v", doc)
	}
	// timeout with cursor intact
	head := doc["cursor"].(string)
	start := time.Now()
	so, _, code = run(t, dir, "watch", "--since", head, "--timeout", "1")
	if code != 2 || time.Since(start) < time.Second {
		t.Fatalf("timeout contract: code=%d", code)
	}
	doc = mustJSON(t, so)
	if doc["timeout"] != true || doc["cursor"] != head {
		t.Fatalf("timeout payload: %v", doc)
	}
}

func TestWatchCursorlessEmitsStart(t *testing.T) {
	dir := seed(t)
	so, _, code := run(t, dir, "watch", "--timeout", "1")
	if code != 2 {
		t.Fatal("no events after head: timeout expected")
	}
	if mustJSON(t, so)["starting_cursor"] == nil {
		t.Fatal("cursorless watch must emit its starting cursor (cold-start rule)")
	}
}

// TestWatchFollowCursorlessEmitsStartLine: --follow's per-event JSON stream
// has no enclosing envelope, so the cold-start announcement (the head a
// crashed/killed follow consumer must resume from) can't ride the final
// drain/timeout payload the way it does in non-follow watch — it needs its
// own leading line. --follow itself loops forever and isn't unit-testable
// (no process control here), so this exercises the pre-loop setup
// (resolveStartCursor) directly with follow=true and a non-TTY Ctx, which
// is the exact call runWatch makes before entering the stream loop.
func TestWatchFollowCursorlessEmitsStartLine(t *testing.T) {
	dir := seed(t)
	var buf bytes.Buffer
	c := &Ctx{Store: store.Store{Repo: gitx.Repo{Dir: dir}}, TTY: false, Stdout: &buf, Stderr: &buf}
	led, err := c.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	cur, start, err := resolveStartCursor(c, led, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if start["starting_cursor"] != cur {
		t.Fatalf("returned start map must carry the resolved cursor: %v (cur=%s)", start, cur)
	}
	doc := mustJSON(t, buf.String())
	if doc["starting_cursor"] != cur {
		t.Fatalf("follow's leading JSON line must carry starting_cursor so a killed consumer can resume: %q", buf.String())
	}
}

func TestWatchValueFilter(t *testing.T) {
	dir := seed(t)
	so, _, _ := run(t, dir, "since", "--limit", "1")
	c0 := mustJSON(t, so)["cursor"].(string)
	so, _, _ = run(t, dir, "watch", "--since", c0, "--value", "done,failed", "--timeout", "5")
	for _, e := range mustJSON(t, so)["events"].([]any) {
		vals := e.(map[string]any)["fields"].(map[string]any)
		found := false
		for _, v := range vals {
			if v == "done" || v == "failed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("filter leak: %v", e)
		}
	}
}

// TestFollowDocIncludesKindAndTextForNotes: --follow's per-event stream
// previously reduced a note to {id,key,fields:null,by,ts} — indistinguishable
// from a set with no fields. Note events must additionally carry kind and a
// text preview, truncated to 200 runes; set events must carry neither.
func TestFollowDocIncludesKindAndTextForNotes(t *testing.T) {
	ev := model.Event{ID: "abc123", Type: "note", Kind: "gotcha", Key: "t1", Author: "x",
		TS: "2026-08-13T00:00:00", Text: strings.Repeat("a", 250)}
	doc := followDoc(ev)
	if doc["kind"] != "gotcha" {
		t.Fatalf("follow doc must carry kind for note events: %v", doc)
	}
	text, _ := doc["text"].(string)
	if r := []rune(text); len(r) != 203 { // 200 runes + "..." marker
		t.Fatalf("follow doc text must be truncated to 200 runes: %d runes: %q", len(r), text)
	}
	if !strings.HasSuffix(text, "...") {
		t.Fatalf("truncated text must carry the ellipsis marker: %q", text)
	}

	setEv := model.Event{ID: "def456", Type: "set", Key: "t1", Author: "x", TS: "2026-08-13T00:00:00",
		Fields: map[string]string{"status": "open"}}
	setDoc := followDoc(setEv)
	if _, ok := setDoc["kind"]; ok {
		t.Fatalf("set events must not carry kind: %v", setDoc)
	}
	if _, ok := setDoc["text"]; ok {
		t.Fatalf("set events must not carry text: %v", setDoc)
	}
}
