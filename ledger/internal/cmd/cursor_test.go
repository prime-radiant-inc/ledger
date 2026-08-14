package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"ledger/internal/gitx"
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
