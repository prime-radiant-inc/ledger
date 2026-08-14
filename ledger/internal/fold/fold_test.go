package fold

import (
	"testing"

	"ledger/internal/model"
)

func ev(id, typ string, mut func(*model.Event)) model.Event {
	e := model.Event{ID: id, TS: "2026-08-13T00:00:0" + id[:1], Type: typ, Author: "a"}
	if mut != nil {
		mut(&e)
	}
	return e
}

func TestFoldSchemaSpineStateLinks(t *testing.T) {
	meta := model.Meta{Slug: "demo", Fields: map[string][]string{"status": {"open", "done"}},
		RequireEvidence: map[string][]string{"status": {"done"}}}
	evs := []model.Event{
		ev("1aaaaaaaaa", "create", nil),
		ev("2aaaaaaaaa", "set", func(e *model.Event) { e.Key = "t1"; e.Fields = map[string]string{"status": "open"} }),
		ev("3aaaaaaaaa", "vocab", func(e *model.Event) { e.Field = "status"; e.Value = "blocked" }),
		ev("4aaaaaaaaa", "set", func(e *model.Event) { e.Key = "t1"; e.Fields = map[string]string{"status": "blocked"} }),
		ev("5aaaaaaaaa", "note", func(e *model.Event) { e.Kind = "handoff"; e.Text = "hi" }),
		ev("6aaaaaaaaa", "lifecycle", func(e *model.Event) { e.LifecycleKind = "close"; e.Reason = "superseded" }),
		ev("7aaaaaaaaa", "lifecycle", func(e *model.Event) { e.LifecycleKind = "superseded_by"; e.Successor = "demo-2" }),
		ev("8aaaaaaaaa", "lifecycle", func(e *model.Event) { e.LifecycleKind = "superseded_by"; e.Successor = "demo-3" }),
	}
	l := Fold("demo", evs, meta)
	if got := l.Schema["status"]; len(got) != 3 || got[2] != "blocked" {
		t.Fatalf("vocab growth: %v", got)
	}
	if l.Spine["t1"]["status"].Fields["status"] != "blocked" {
		t.Fatalf("spine latest: %+v", l.Spine["t1"]["status"])
	}
	if l.State != "closed:superseded" {
		t.Fatalf("state: %q", l.State)
	}
	if l.SupersededBy != "demo-2" || len(l.ExtraLinks) != 1 || l.ExtraLinks[0] != "demo-3" {
		t.Fatalf("links: %q %v (first in total order wins; later links flagged)", l.SupersededBy, l.ExtraLinks)
	}
	if len(l.Notes()) != 1 || l.Head() != "8aaaaaaaaa" {
		t.Fatalf("notes/head")
	}
}

func TestFoldFreeFieldAndDefaults(t *testing.T) {
	meta := model.Meta{Fields: map[string][]string{"tag": nil}}
	l := Fold("x", []model.Event{ev("1aaaaaaaaa", "create", nil)}, meta)
	if v, ok := l.Schema["tag"]; !ok || v != nil {
		t.Fatalf("free field must fold as nil vocab: %v %v", v, ok)
	}
	if l.State != "open" || len(l.Spine) != 0 {
		t.Fatalf("empty fold: %+v", l)
	}
}

func TestFoldSyncEventSkipped(t *testing.T) {
	meta := model.Meta{Fields: map[string][]string{"status": {"open"}}}
	evs := []model.Event{
		ev("1aaaaaaaaa", "create", nil),
		ev("2aaaaaaaaa", "set", func(e *model.Event) { e.Key = "t1"; e.Fields = map[string]string{"status": "open"} }),
		ev("3aaaaaaaaa", "sync", nil), // sync event should be completely invisible
		ev("4aaaaaaaaa", "set", func(e *model.Event) { e.Key = "t1"; e.Fields = map[string]string{"status": "open"} }),
	}
	l := Fold("demo", evs, meta)
	// Sync event should be skipped, not affecting schema, spine, or state
	if len(l.Schema["status"]) != 1 || l.Schema["status"][0] != "open" {
		t.Fatalf("sync should not affect schema: %v", l.Schema["status"])
	}
	if l.Spine["t1"]["status"].ID != "4aaaaaaaaa" {
		t.Fatalf("sync should not affect spine: got ID %q", l.Spine["t1"]["status"].ID)
	}
	if l.State != "open" {
		t.Fatalf("sync should not affect state: %q", l.State)
	}
}

func TestFoldDuelingCloses(t *testing.T) {
	meta := model.Meta{}
	evs := []model.Event{
		ev("1aaaaaaaaa", "create", nil),
		ev("2aaaaaaaaa", "lifecycle", func(e *model.Event) { e.LifecycleKind = "close"; e.Reason = "wont_do" }),
		ev("3aaaaaaaaa", "lifecycle", func(e *model.Event) { e.LifecycleKind = "close"; e.Reason = "duplicate" }),
	}
	l := Fold("demo", evs, meta)
	// First close in total order wins; second close should not overwrite State
	if l.State != "closed:wont_do" {
		t.Fatalf("dueling closes: first should win, got %q", l.State)
	}
}

func TestFoldVocabForFreeField(t *testing.T) {
	meta := model.Meta{Fields: map[string][]string{"tag": nil}}
	evs := []model.Event{
		ev("1aaaaaaaaa", "create", nil),
		ev("2aaaaaaaaa", "vocab", func(e *model.Event) { e.Field = "tag"; e.Value = "urgent" }),
	}
	l := Fold("demo", evs, meta)
	// Vocab event for a free field (nil vocab) should be a no-op
	if len(l.Schema["tag"]) != 0 || l.Schema["tag"] != nil {
		t.Fatalf("vocab for free field should be no-op, got %v", l.Schema["tag"])
	}
}

func TestFoldVocabForUndeclaredField(t *testing.T) {
	meta := model.Meta{Fields: map[string][]string{"status": {"open"}}}
	evs := []model.Event{
		ev("1aaaaaaaaa", "create", nil),
		ev("2aaaaaaaaa", "vocab", func(e *model.Event) { e.Field = "priority"; e.Value = "high" }),
	}
	l := Fold("demo", evs, meta)
	// Vocab event for an undeclared field should be a no-op
	if _, ok := l.Schema["priority"]; ok {
		t.Fatalf("vocab for undeclared field should be no-op, got %v", l.Schema["priority"])
	}
	if len(l.Schema["status"]) != 1 || l.Schema["status"][0] != "open" {
		t.Fatalf("schema should only have original fields, got %v", l.Schema)
	}
}
