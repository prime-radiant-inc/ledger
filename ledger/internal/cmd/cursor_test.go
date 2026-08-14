package cmd

import (
	"strings"
	"testing"
	"time"
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
