package cmd

import (
	"strings"
	"testing"
	"time"

	"ledger/internal/fold"
	"ledger/internal/model"
)

// TestLastEventTimeParsesMillisecondLayout: new events are written with
// model.TSLayout (millisecond precision). lastEventTime must age them
// correctly, not fall back to the zero time.
func TestLastEventTimeParsesMillisecondLayout(t *testing.T) {
	want := time.Now().UTC().Add(-90 * time.Minute)
	led := &fold.Ledger{Events: []model.Event{
		{TS: want.Format(model.TSLayout)},
	}}
	got := lastEventTime(led)
	if got.IsZero() {
		t.Fatal("lastEventTime must parse millisecond-layout timestamps, got zero time")
	}
	if diff := got.Sub(want); diff < -time.Second || diff > time.Second {
		t.Fatalf("lastEventTime: got %v, want ~%v (diff %v)", got, want, diff)
	}
}

// TestLastEventTimeParsesLegacyLayout: old events (no fractional seconds)
// must still parse.
func TestLastEventTimeParsesLegacyLayout(t *testing.T) {
	want := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
	led := &fold.Ledger{Events: []model.Event{
		{TS: want.Format(model.TSLayoutLegacy)},
	}}
	got := lastEventTime(led)
	if !got.Equal(want) {
		t.Fatalf("lastEventTime (legacy): got %v, want %v", got, want)
	}
}

func TestLsEmptyAnnounces(t *testing.T) {
	dir := initRepo(t)
	so, _, code := run(t, dir, "ls")
	if code != 0 {
		t.Fatal(so)
	}
	doc := mustJSON(t, so)
	if l := doc["ledgers"].([]any); len(l) != 0 {
		t.Fatalf("%v", l)
	}
}

func TestLsFiltersAndSort(t *testing.T) {
	dir := initRepo(t)
	run(t, dir, "create", "one", "--scope", "a")
	run(t, dir, "create", "two", "--scope", "b")
	run(t, dir, "close", "two", "--as-state", "shipped")
	so, _, _ := run(t, dir, "ls")
	doc := mustJSON(t, so)
	l := doc["ledgers"].([]any)
	if len(l) != 2 { // closed-within-30d stays visible
		t.Fatalf("%v", l)
	}
	so, _, _ = run(t, dir, "ls", "--all")
	if len(mustJSON(t, so)["ledgers"].([]any)) != 2 {
		t.Fatal("--all")
	}
	states := []string{}
	for _, e := range l {
		states = append(states, e.(map[string]any)["state"].(string))
	}
	if !strings.HasPrefix(states[0], "open") && !strings.HasPrefix(states[1], "open") {
		t.Fatalf("open ledgers present: %v", states)
	}
}

// TestLsClosedCutoffExcludesOldClosed: a closed ledger older than
// lsClosedCutoff drops out of the default listing but stays reachable with
// --all. lsClosedCutoff is a package var expressly so this doesn't need a
// fake clock — it's overridden down to a window the freshly-closed ledger
// above is guaranteed to fall outside of.
func TestLsClosedCutoffExcludesOldClosed(t *testing.T) {
	old := lsClosedCutoff
	lsClosedCutoff = 0
	defer func() { lsClosedCutoff = old }()

	dir := initRepo(t)
	run(t, dir, "create", "one", "--scope", "a")
	run(t, dir, "create", "two", "--scope", "b")
	run(t, dir, "close", "two", "--as-state", "shipped")

	so, _, _ := run(t, dir, "ls")
	doc := mustJSON(t, so)
	l := doc["ledgers"].([]any)
	if len(l) != 1 {
		t.Fatalf("closed-outside-cutoff should be hidden by default: %v", l)
	}
	if l[0].(map[string]any)["slug"] != "one" {
		t.Fatalf("wrong survivor: %v", l)
	}

	so, _, _ = run(t, dir, "ls", "--all")
	if len(mustJSON(t, so)["ledgers"].([]any)) != 2 {
		t.Fatal("--all should still show it")
	}
}

// TestLsIdleMarking: lsIdleAfter is a package var for the same reason —
// overriding it to 0 forces any open ledger to read as idle without waiting
// on a real clock.
func TestLsIdleMarking(t *testing.T) {
	old := lsIdleAfter
	lsIdleAfter = 0
	defer func() { lsIdleAfter = old }()

	dir := initRepo(t)
	run(t, dir, "create", "one", "--scope", "a")

	so, _, _ := run(t, dir, "ls")
	doc := mustJSON(t, so)
	l := doc["ledgers"].([]any)
	if len(l) != 1 {
		t.Fatalf("%v", l)
	}
	row := l[0].(map[string]any)
	if idle, _ := row["idle"].(bool); !idle {
		t.Fatalf("expected idle:true, got %v", row)
	}
}

func TestLsEmptyAfterFilterAnnounces(t *testing.T) {
	old := lsClosedCutoff
	lsClosedCutoff = 0
	defer func() { lsClosedCutoff = old }()

	dir := initRepo(t)
	run(t, dir, "create", "one", "--scope", "a")
	run(t, dir, "close", "one", "--as-state", "shipped")

	so, se, code := run(t, dir, "ls")
	if code != 0 {
		t.Fatal(se)
	}
	doc := mustJSON(t, so)
	l := doc["ledgers"].([]any)
	if len(l) != 0 {
		t.Fatalf("%v", l)
	}
	// --all still surfaces it.
	so, _, _ = run(t, dir, "ls", "--all")
	if len(mustJSON(t, so)["ledgers"].([]any)) != 1 {
		t.Fatal("--all")
	}
}
