package board

import (
	"testing"
	"time"

	"ledger/internal/model"
)

func setEv(id, key, field, value string, mut func(*model.Event)) model.Event {
	e := model.Event{ID: id, Type: "set", Key: key, Author: "a",
		TS: "2026-08-16T00:00:00.000", Fields: map[string]string{field: value}}
	if mut != nil {
		mut(&e)
	}
	return e
}

func readyMeta() model.Meta {
	return model.Meta{
		Fields:      map[string][]string{"status": {"open", "in-progress", "closed"}},
		Terminal:    map[string][]string{"status": {"closed"}},
		MultiFields: []string{"labels", "blocked-by"},
	}
}

// TestTitleFromFirstStatusEventSurvivesLaterSets: title is the Text of the
// FIRST event that sets status — later status writes (claim, close) must
// never overwrite it, even though they do update Status.Value.
func TestTitleFromFirstStatusEventSurvivesLaterSets(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "fix-retry", "status", "open", func(e *model.Event) { e.Text = "fix the retry loop" }),
		setEv("2a", "fix-retry", "status", "in-progress", func(e *model.Event) { e.Text = "claiming" }),
		setEv("3a", "fix-retry", "status", "closed", func(e *model.Event) { e.Text = "done" }),
	}
	b := Build(readyMeta(), evs)
	k := b.Keys["fix-retry"]
	if k == nil {
		t.Fatal("key missing")
	}
	if k.Title != "fix the retry loop" {
		t.Fatalf("title: got %q, want first status event's text", k.Title)
	}
	if k.Status.Value != "closed" || k.Status.ID != "3a" || k.Status.Note != "done" {
		t.Fatalf("status not latest: %+v", k.Status)
	}
}

// TestLabelClearViaEmptyValue: labels= (empty value) clears the labels set.
func TestLabelClearViaEmptyValue(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "k1", "labels", "urgent,human", nil),
		setEv("2a", "k1", "labels", "", func(e *model.Event) { e.ID = "2a" }),
	}
	b := Build(readyMeta(), evs)
	k := b.Keys["k1"]
	if len(k.Labels) != 0 {
		t.Fatalf("labels should be cleared, got %v", k.Labels)
	}
	if k.LabelsID != "2a" {
		t.Fatalf("LabelsID should track latest labels event, got %q", k.LabelsID)
	}
}

// TestStaleAgeMixedTimestampLayouts: spec test 15's mixed-precision clause —
// a legacy-second-layout claim event must still age correctly against a
// millisecond `now`.
func TestStaleAgeMixedTimestampLayouts(t *testing.T) {
	meta := readyMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		setEv("1a", "k1", "status", "in-progress", func(e *model.Event) {
			e.TS = "2026-08-16T10:00:00" // legacy layout, no milliseconds
		}),
	}
	b := Build(meta, evs)
	k := b.Keys["k1"]
	now, err := model.ParseTS("2026-08-16T12:00:00.000")
	if err != nil {
		t.Fatal(err)
	}
	stale, age := b.StaleAge(k, now)
	if !stale {
		t.Fatalf("expected stale (2h claim, 1h horizon), got age=%v", age)
	}
	if age != 2*time.Hour {
		t.Fatalf("age: got %v, want 2h", age)
	}
}

// TestStaleAgeSubSecondHorizon: a 500ms --stale-after with a 1s-old claim
// must report stale — staleness math must work below whole-second
// resolution.
func TestStaleAgeSubSecondHorizon(t *testing.T) {
	meta := readyMeta()
	meta.StaleAfter = "500ms"
	evs := []model.Event{
		setEv("1a", "k1", "status", "in-progress", func(e *model.Event) {
			e.TS = "2026-08-16T12:00:00.000"
		}),
	}
	b := Build(meta, evs)
	k := b.Keys["k1"]
	now, _ := model.ParseTS("2026-08-16T12:00:01.000")
	stale, age := b.StaleAge(k, now)
	if !stale {
		t.Fatalf("expected stale (1s claim, 500ms horizon), got age=%v", age)
	}
	if age != time.Second {
		t.Fatalf("age: got %v, want 1s", age)
	}
}

// TestStaleAgeNotInProgressNeverStale: a closed key's status is never
// stale, no matter its age, and StaleAfter="" (undeclared) means never
// stale either.
func TestStaleAgeNotInProgressNeverStale(t *testing.T) {
	meta := readyMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		setEv("1a", "k1", "status", "closed", func(e *model.Event) {
			e.TS = "2026-08-16T00:00:00.000"
		}),
	}
	b := Build(meta, evs)
	k := b.Keys["k1"]
	now, _ := model.ParseTS("2026-08-16T12:00:00.000")
	if stale, _ := b.StaleAge(k, now); stale {
		t.Fatal("closed key must never be stale")
	}

	meta2 := readyMeta() // no StaleAfter declared
	evs2 := []model.Event{
		setEv("1a", "k2", "status", "in-progress", func(e *model.Event) {
			e.TS = "2026-08-16T00:00:00.000"
		}),
	}
	b2 := Build(meta2, evs2)
	if stale, _ := b2.StaleAge(b2.Keys["k2"], now); stale {
		t.Fatal("no --stale-after declared means never stale")
	}
}

// TestHasHumanMultiTokenLabels: "human" must be recognized as one token
// among several, not just as the sole label.
func TestHasHumanMultiTokenLabels(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "k1", "labels", "urgent,human", nil),
		setEv("1b", "k2", "labels", "urgent", nil),
	}
	b := Build(readyMeta(), evs)
	if !b.Keys["k1"].HasHuman() {
		t.Fatal("k1 should carry human (multi-token label)")
	}
	if b.Keys["k2"].HasHuman() {
		t.Fatal("k2 should not carry human")
	}
}

// TestStatuslessKeyHasNilStatus: a key that only ever received edge/label
// events (no status write) must report Status == nil and Title == "".
func TestStatuslessKeyHasNilStatus(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "half-seeded", "blocked-by", "other-key", nil),
	}
	b := Build(readyMeta(), evs)
	k := b.Keys["half-seeded"]
	if k == nil {
		t.Fatal("key missing")
	}
	if k.Status != nil {
		t.Fatalf("statusless key must have nil Status, got %+v", k.Status)
	}
	if k.Title != "" {
		t.Fatalf("statusless key must have empty title, got %q", k.Title)
	}
	if len(k.BlockedBy) != 1 || k.BlockedBy[0] != "other-key" {
		t.Fatalf("blocked-by not parsed: %v", k.BlockedBy)
	}
	if k.BlockedByID != "1a" {
		t.Fatalf("BlockedByID: got %q", k.BlockedByID)
	}
}

func TestIsTerminal(t *testing.T) {
	b := Build(readyMeta(), nil)
	if !b.IsTerminal("closed") {
		t.Fatal("closed should be terminal per meta")
	}
	if b.IsTerminal("open") {
		t.Fatal("open should not be terminal")
	}
}

func TestFormatAge(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "0s"},
		{90 * time.Second, "1m30s"},
		{2*time.Hour + 30*time.Minute, "2h30m0s"},
	} {
		if got := FormatAge(tc.d); got != tc.want {
			t.Errorf("FormatAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
