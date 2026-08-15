package fold

import (
	"fmt"
	"math/rand"
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

func rollupEv(id, author string, children ...string) model.Event {
	return model.Event{TS: "2026-08-14T00:00:0" + id[len(id)-1:], Type: "rollup",
		Author: author, Text: "summary " + id, Children: children, ID: id}
}

func TestRollupFoldParentAndState(t *testing.T) {
	evs := []model.Event{
		{TS: "2026-08-14T00:00:01", Type: "create", Author: "a", ID: "e1"},
		{TS: "2026-08-14T00:00:02", Type: "set", Key: "k", Fields: map[string]string{"status": "done"}, Author: "a", ID: "e2"},
		rollupEv("r1", "curator", "e1", "e2"),
	}
	l := Fold("s", evs, model.Meta{Fields: map[string][]string{"status": {"open", "done"}}})
	if l.Parent["e1"] != "r1" || l.Parent["e2"] != "r1" {
		t.Fatalf("parent map wrong: %v", l.Parent)
	}
	// state fold ignores rollups: spine still has k=done, state still open
	if l.Spine["k"]["status"].ID != "e2" || l.State != "open" {
		t.Fatalf("rollup leaked into state fold")
	}
}

func TestRollupDuelAllOrNothing(t *testing.T) {
	evs := []model.Event{
		{TS: "2026-08-14T00:00:01", Type: "create", Author: "a", ID: "e1"},
		{TS: "2026-08-14T00:00:02", Type: "note", Kind: "note", Text: "x", Author: "a", ID: "e2"},
		{TS: "2026-08-14T00:00:03", Type: "note", Kind: "note", Text: "y", Author: "a", ID: "e3"},
		rollupEv("r1", "a", "e2"),
		rollupEv("r2", "b", "e2", "e3"), // e2 already taken → r2 loses WHOLLY: e3 stays loose
	}
	l := Fold("s", evs, model.Meta{})
	if !l.Losers["r2"] {
		t.Fatalf("r2 must be a duel loser")
	}
	if _, taken := l.Parent["e3"]; taken {
		t.Fatalf("all-or-nothing: losing rollup must not claim e3")
	}
	roots := l.Roots()
	for _, r := range roots {
		if r.ID == "r2" {
			t.Fatalf("loser rollup must not be a root")
		}
	}
	if l.Due() != 2 { // e1, e3 loose + ... e2 is inside r1; roots: e1, r1, e3 → non-rollup roots = e1, e3 = 2
		// (deliberate arithmetic check — fix the expected value to match the rule:
		// Due counts non-rollup roots; here that is e1 and e3)
		t.Fatalf("due = %d", l.Due())
	}
}

func TestRootsCausalOrder(t *testing.T) {
	// live event e4 lands BEFORE the rollup commit r1 that encapsulates the
	// earlier e2,e3 thread; causal order must put r1 (earliest base e2) first.
	evs := []model.Event{
		{TS: "2026-08-14T00:00:01", Type: "create", Author: "a", ID: "e1"},
		{TS: "2026-08-14T00:00:02", Type: "note", Kind: "note", Text: "t", Author: "a", ID: "e2"},
		{TS: "2026-08-14T00:00:03", Type: "note", Kind: "note", Text: "t", Author: "a", ID: "e3"},
		{TS: "2026-08-14T00:00:04", Type: "set", Key: "live", Fields: map[string]string{"status": "open"}, Author: "a", ID: "e4"},
		rollupEv("r1", "curator", "e2", "e3"),
	}
	l := Fold("s", evs, model.Meta{Fields: map[string][]string{"status": {"open"}}})
	roots := l.Roots()
	ids := []string{}
	for _, r := range roots {
		ids = append(ids, r.ID)
	}
	want := []string{"e1", "r1", "e4"}
	if len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("causal order wrong: got %v want %v", ids, want)
	}
}

func TestRecursiveRollupAndCoverage(t *testing.T) {
	// Coverage invariant (spec test 41): every non-sentinel event is either a
	// root or transitively inside exactly one visible root. Randomly roll a
	// 500-event chain and check.
	evs := []model.Event{{TS: "2026-08-14T00:00:00", Type: "create", Author: "a", ID: "e0"}}
	for i := 1; i < 500; i++ {
		evs = append(evs, model.Event{TS: "2026-08-14T00:00:01", Type: "note", Kind: "note",
			Text: "n", Author: "a", ID: fmt.Sprintf("e%d", i)})
	}
	rng := rand.New(rand.NewSource(41))
	loose := []string{}
	for _, e := range evs {
		loose = append(loose, e.ID)
	}
	for r := 0; r < 60; r++ { // 60 random rollups, each eating 2-9 loose records
		n := 2 + rng.Intn(8)
		if len(loose) < n+1 {
			break
		}
		var kids []string
		for j := 0; j < n; j++ {
			k := rng.Intn(len(loose))
			kids = append(kids, loose[k])
			loose = append(loose[:k], loose[k+1:]...)
		}
		id := fmt.Sprintf("r%d", r)
		evs = append(evs, rollupEv(id, "c", kids...))
		loose = append(loose, id)
	}
	l := Fold("s", evs, model.Meta{})
	// walk down from every root; every event must be reached exactly once
	seen := map[string]int{}
	byID := map[string]model.Event{}
	for _, e := range l.Events {
		byID[e.ID] = e
	}
	var walk func(id string)
	walk = func(id string) {
		seen[id]++
		if e := byID[id]; e.Type == "rollup" {
			for _, c := range e.Children {
				walk(c)
			}
		}
	}
	for _, r := range l.Roots() {
		walk(r.ID)
	}
	for _, e := range l.Events {
		if seen[e.ID] != 1 {
			t.Fatalf("event %s reached %d times (coverage invariant)", e.ID, seen[e.ID])
		}
	}
}
