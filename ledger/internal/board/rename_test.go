package board

import (
	"testing"

	"ledger/internal/model"
)

// renameEv builds one rename event: an otherwise-empty "set" carrying the
// new title on its own top-level field.
func renameEv(id, key, title, author string) model.Event {
	return model.Event{ID: id, Type: "set", Key: key, Author: author,
		TS: "2026-08-16T00:00:00.000", Rename: title}
}

// TestTitleFoldNoneOneSeveral is the fold rule: a key's title is the latest
// rename event's text in fold order, else the first status event's -m. Zero,
// one and several renames.
func TestTitleFoldNoneOneSeveral(t *testing.T) {
	seed := setEv("1a", "fix-retry", "status", "open", func(e *model.Event) { e.Text = "fix teh retry lop" })

	none := Build(readyMeta(), []model.Event{seed}).Keys["fix-retry"]
	if none.Title != "fix teh retry lop" {
		t.Fatalf("unrenamed key keeps the seed title: %q", none.Title)
	}
	if none.RenameInfo() != nil {
		t.Fatalf("the renamed structure is ABSENT on an unrenamed key: %+v", none.RenameInfo())
	}

	one := Build(readyMeta(), []model.Event{seed,
		renameEv("2a", "fix-retry", "fix the retry loop", "kit")}).Keys["fix-retry"]
	if one.Title != "fix the retry loop" {
		t.Fatalf("one rename titles the key: %q", one.Title)
	}
	info := one.RenameInfo()
	if info == nil || info.By != "kit" || info.ID != "2a" {
		t.Fatalf("renamed info must name the latest rename's author and id: %+v", info)
	}
	if len(info.Prior) != 1 || info.Prior[0] != "fix teh retry lop" {
		t.Fatalf("prior carries the fold-path seed title: %v", info.Prior)
	}

	several := Build(readyMeta(), []model.Event{seed,
		renameEv("2a", "fix-retry", "fix the retry loop", "kit"),
		renameEv("3a", "fix-retry", "fix the retry storm", "ash"),
	}).Keys["fix-retry"]
	if several.Title != "fix the retry storm" {
		t.Fatalf("the LAST rename in fold order wins: %q", several.Title)
	}
	info = several.RenameInfo()
	if info.By != "ash" || info.ID != "3a" {
		t.Fatalf("renamed info follows the latest rename: %+v", info)
	}
	want := []string{"fix teh retry lop", "fix the retry loop"}
	if len(info.Prior) != len(want) {
		t.Fatalf("prior lists every earlier title oldest first: %v", info.Prior)
	}
	for i := range want {
		if info.Prior[i] != want[i] {
			t.Fatalf("prior[%d]: got %q want %q (%v)", i, info.Prior[i], want[i], info.Prior)
		}
	}
}

// TestTitleSurvivesClaimAndCloseAndNeverResurrectsTheSeedMessage: later
// status writes update Status but never the title — neither their own -m nor
// the seed's can displace a landed rename.
func TestTitleSurvivesClaimAndCloseAndNeverResurrectsTheSeedMessage(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "fix-retry", "status", "open", func(e *model.Event) { e.Text = "seed message" }),
		renameEv("2a", "fix-retry", "the real title", "kit"),
		setEv("3a", "fix-retry", "status", "in-progress", func(e *model.Event) { e.Text = "claiming" }),
		setEv("4a", "fix-retry", "status", "closed", func(e *model.Event) { e.Text = "done" }),
	}
	k := Build(readyMeta(), evs).Keys["fix-retry"]
	if k.Title != "the real title" {
		t.Fatalf("a rename survives claim and close: %q", k.Title)
	}
	if k.Status.Value != "closed" || k.Status.Note != "done" {
		t.Fatalf("status still folds normally: %+v", k.Status)
	}
}

// TestTitleFoldRenamePrecedingSeed is the reachable half of fold totality: a
// rename sitting BEFORE a colliding seed in fold order (what a two-root merge
// leaves behind) still titles the key, and prior carries the fold-path seed
// even though that seed folds later.
func TestTitleFoldRenamePrecedingSeed(t *testing.T) {
	evs := []model.Event{
		renameEv("1a", "task-signup", "signup flow, kit's read", "kit"),
		setEv("2a", "task-signup", "status", "open", func(e *model.Event) { e.Text = "ash's seed title" }),
	}
	k := Build(readyMeta(), evs).Keys["task-signup"]
	if k.Title != "signup flow, kit's read" {
		t.Fatalf("the latest RENAME titles the key regardless of the seed's fold position: %q", k.Title)
	}
	info := k.RenameInfo()
	if info == nil || len(info.Prior) != 1 || info.Prior[0] != "ash's seed title" {
		t.Fatalf("prior carries the fold-path seed: %+v", info)
	}
}

// TestTitleFoldNoSeedTotality is the hand-built half: a rename with NO status
// event anywhere in the chain still titles the key. Fixture-crafted on
// purpose — sync merges whole chains and cannot ship a rename without its
// seed — but the fold must be total, never crash or fall back to "".
func TestTitleFoldNoSeedTotality(t *testing.T) {
	k := Build(readyMeta(), []model.Event{renameEv("1a", "orphan", "titled with no seed", "kit")}).Keys["orphan"]
	if k == nil {
		t.Fatal("a rename-only key must exist on the board")
	}
	if k.Status != nil {
		t.Fatalf("a rename never gives a key a status: %+v", k.Status)
	}
	if k.Title != "titled with no seed" {
		t.Fatalf("a seedless rename still titles the key: %q", k.Title)
	}
	if info := k.RenameInfo(); info == nil || len(info.Prior) != 0 {
		t.Fatalf("with no seed there is no prior title: %+v", info)
	}
}

// TestTwoRootCollisionLosingSeedTitleAppearsNowhere: under a two-root
// collision the FIRST status event in fold order is the fold-path seed; the
// losing root's seed title appears in no rename structure at all. The
// read-both-heads doctrine is the only way to see it — the skill says so.
func TestTwoRootCollisionLosingSeedTitleAppearsNowhere(t *testing.T) {
	evs := []model.Event{
		setEv("1a", "task-signup", "status", "open", func(e *model.Event) { e.Text = "ash's seed" }),
		setEv("2a", "task-signup", "status", "open", func(e *model.Event) { e.Text = "kit's colliding seed" }),
		renameEv("3a", "task-signup", "agreed title", "ash"),
	}
	info := Build(readyMeta(), evs).Keys["task-signup"].RenameInfo()
	if len(info.Prior) != 1 || info.Prior[0] != "ash's seed" {
		t.Fatalf("prior is fold-path history, not a complete inventory: %v", info.Prior)
	}
}
