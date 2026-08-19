package board

import (
	"reflect"
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

// ---- the contested title stream (rename events as pseudo-field "title") ----

// renameAt is a fixture rename event with a chosen author and timestamp.
func renameAt(id, key, title, author, ts string) model.Event {
	return model.Event{ID: id, Type: "set", Key: key, Author: author, TS: ts, Rename: title}
}

// TestConcurrentRenamesContestAsTheTitlePseudoField: the rename stream is a
// write-head antichain like any guarded field's — same definition, ids
// fold-ordered winner-last, expect = the winner's id (usable verbatim as
// `--rename --expect`).
func TestConcurrentRenamesContestAsTheTitlePseudoField(t *testing.T) {
	evs := []model.Event{
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		renameAt("alicerename", "t1", "alice's title", "alice", "2026-08-17T11:00:00.000"),
		renameAt("bobrename0", "t1", "bob's title", "bob", "2026-08-17T12:00:00.000"),
	}
	evs[0].Text = "the seed title"
	d := dagOf(evs, [][]int{{}, {0}, {0}})
	b := Build(contestMeta(), evs)
	b.ComputeContests(evs, d)

	c := contestOn(t, b, "t1", TitleField)
	if !reflect.DeepEqual(c.IDs, []string{"alicerename", "bobrename0"}) {
		t.Fatalf("heads in fold order, winner last: %v", c.IDs)
	}
	if !reflect.DeepEqual(c.Authors, []string{"alice", "bob"}) {
		t.Fatalf("authors parallel to ids: %v", c.Authors)
	}
	if c.Expect != "bobrename0" {
		t.Fatalf("expect is the winner's id: %q", c.Expect)
	}
	if b.Keys["t1"].Title != "bob's title" {
		t.Fatalf("the fold winner and the contest winner are the same event: %q", b.Keys["t1"].Title)
	}
}

// TestTitleContestCollapsesLikeAnyOtherStream: a third rename descending
// from both heads collapses the antichain — no clearing rule, the definition
// clears itself — and ResolvedHeads names the losers it beat.
func TestTitleContestCollapsesLikeAnyOtherStream(t *testing.T) {
	evs := []model.Event{
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		renameAt("alicerename", "t1", "alice's title", "alice", "2026-08-17T11:00:00.000"),
		renameAt("bobrename0", "t1", "bob's title", "bob", "2026-08-17T12:00:00.000"),
		renameAt("collapse00", "t1", "the agreed title", "kit", "2026-08-17T13:00:00.000"),
	}
	evs[0].Text = "the seed title"
	d := dagOf(evs, [][]int{{}, {0}, {0}, {1, 2}})

	losers := ResolvedHeads(evs[:3], dagOf(evs[:3], [][]int{{}, {0}, {0}}), "t1", TitleField)
	if !reflect.DeepEqual(losers, []string{"alicerename"}) {
		t.Fatalf("the collapsing write records every head but the winner: %v", losers)
	}

	b := Build(contestMeta(), evs)
	b.ComputeContests(evs, d)
	if len(b.Contests["t1"]) != 0 {
		t.Fatalf("the collapse clears the contest: %+v", b.Contests["t1"])
	}
}

// TestTitleStreamContestsOnAGuardFreeReadyCapableBoard pins the pass's scope
// rule directly: guards do not bound the title stream. No minting path can
// produce this meta (create/import/adopt all require --guard status on a
// ready-capable board), so it is hand-built here — this package is pure and
// folds whatever meta it is handed, and the removed `len(Guard)==0`
// short-circuit would silence a stream it never bounded.
func TestTitleStreamContestsOnAGuardFreeReadyCapableBoard(t *testing.T) {
	meta := contestMeta()
	meta.Guard = nil
	evs := []model.Event{
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		renameAt("alicerename", "t1", "alice's title", "alice", "2026-08-17T11:00:00.000"),
		renameAt("bobrename0", "t1", "bob's title", "bob", "2026-08-17T12:00:00.000"),
	}
	evs[0].Text = "the seed title"
	b := Build(meta, evs)
	b.ComputeContests(evs, dagOf(evs, [][]int{{}, {0}, {0}}))
	if c := contestOn(t, b, "t1", TitleField); c.Expect != "bobrename0" {
		t.Fatalf("guard-free board still contests its title stream: %+v", c)
	}
}

// TestPlainBoardNeverContestsTitles: titles exist only on ready-capable
// boards, so the pass's scope is unchanged in that dimension.
func TestPlainBoardNeverContestsTitles(t *testing.T) {
	meta := model.Meta{Fields: map[string][]string{"status": {"open", "done"}}}
	evs := []model.Event{
		renameAt("alicerename", "t1", "alice's title", "alice", "2026-08-17T11:00:00.000"),
		renameAt("bobrename0", "t1", "bob's title", "bob", "2026-08-17T12:00:00.000"),
	}
	b := Build(meta, evs)
	b.ComputeContests(evs, dagOf(evs, [][]int{{}, {}}))
	if len(b.Contests) != 0 {
		t.Fatalf("a plain board has no titles to contest: %+v", b.Contests)
	}
}

// TestTitleContestSurfacesInAttentionOnly is the attention-only ruling: a
// title contest raises its attention entry, but it must NOT set per-entry
// `contested: true` and must NOT flip the frontier off all-handled — a
// cosmetic cross-replica retitle can't hold a fleet in the picking loop.
func TestTitleContestSurfacesInAttentionOnly(t *testing.T) {
	evs := []model.Event{
		ev("seed000000", "t1", "status", "in-progress", "alice", "2026-08-17T10:00:00.000"),
		renameAt("alicerename", "t1", "alice's title", "alice", "2026-08-17T11:00:00.000"),
		renameAt("bobrename0", "t1", "bob's title", "bob", "2026-08-17T12:00:00.000"),
	}
	evs[0].Text = "the seed title"
	b := Build(contestMeta(), evs)
	b.ComputeContests(evs, dagOf(evs, [][]int{{}, {0}, {0}}))
	env := b.Envelope(mustParseTS("2026-08-17T13:00:00.000"), 0, alwaysTrue)

	if env.Frontier != "all-handled" {
		t.Fatalf("a title contest must not flip the frontier: %q (%+v)", env.Frontier, env.Attention)
	}
	if len(env.Held) != 1 || env.Held[0].Contested {
		t.Fatalf("a title contest must not set the per-entry contested flag: %+v", env.Held)
	}
	var titleEntries int
	for _, a := range env.Attention {
		if a.Reason == "contested" && a.Contest.Field == TitleField {
			titleEntries++
			if a.Title != "bob's title" {
				t.Fatalf("the entry carries the CURRENT title: %q", a.Title)
			}
			if a.Renamed == nil || a.Renamed.By != "bob" {
				t.Fatalf("the entry carries renamed info: %+v", a.Renamed)
			}
		}
	}
	if titleEntries != 1 {
		t.Fatalf("exactly one title-contest attention entry: %+v", env.Attention)
	}
}

// TestStatusContestStillFlipsTheFrontierAlongsideATitleContest: the
// attention-only rule is scoped to the title stream alone — a guarded-field
// contest on the same board still sets the flag and still moves the verdict.
func TestStatusContestStillFlipsTheFrontierAlongsideATitleContest(t *testing.T) {
	evs := []model.Event{
		ev("seed000000", "t1", "status", "in-progress", "seeder", "2026-08-17T10:00:00.000"),
		ev("aclaim0000", "t1", "status", "in-progress", "alice", "2026-08-17T11:00:00.000"),
		ev("bclaim0000", "t1", "status", "in-progress", "bob", "2026-08-17T11:30:00.000"),
		renameAt("alicerename", "t1", "alice's title", "alice", "2026-08-17T12:00:00.000"),
		renameAt("bobrename0", "t1", "bob's title", "bob", "2026-08-17T12:30:00.000"),
	}
	evs[0].Text = "the seed title"
	b := Build(contestMeta(), evs)
	b.ComputeContests(evs, dagOf(evs, [][]int{{}, {0}, {0}, {1}, {2}}))
	env := b.Envelope(mustParseTS("2026-08-17T13:00:00.000"), 0, alwaysTrue)

	if env.Frontier != "attention-needed" {
		t.Fatalf("a status contest still moves the verdict: %q", env.Frontier)
	}
	if len(env.Held) != 1 || !env.Held[0].Contested {
		t.Fatalf("a status contest still sets the per-entry flag: %+v", env.Held)
	}
}

// TestStatuslessKeyWithAFoldTotalRenameTitlesAllItsEntries: entry titles
// exist whenever the KEY has a title — one title per key per envelope,
// statusless and contested entries alike; `title` is omitted only when no
// title exists at all.
func TestStatuslessKeyWithAFoldTotalRenameTitlesAllItsEntries(t *testing.T) {
	evs := []model.Event{
		renameAt("alicerename", "t1", "alice's title", "alice", "2026-08-17T11:00:00.000"),
		renameAt("bobrename0", "t1", "bob's title", "bob", "2026-08-17T12:00:00.000"),
	}
	b := Build(contestMeta(), evs)
	b.ComputeContests(evs, dagOf(evs, [][]int{{}, {}}))
	env := b.Envelope(mustParseTS("2026-08-17T13:00:00.000"), 0, alwaysTrue)

	seen := 0
	for _, a := range env.Attention {
		if a.Key != "t1" {
			continue
		}
		seen++
		if a.Title != "bob's title" {
			t.Fatalf("%s entry must carry the key's title: %+v", a.Reason, a)
		}
		if a.Renamed == nil {
			t.Fatalf("%s entry must carry renamed info: %+v", a.Reason, a)
		}
	}
	if seen != 2 { // statusless + contested
		t.Fatalf("expected a statusless and a contested entry: %+v", env.Attention)
	}
}

// TestStatuslessKeyWithNoTitleOmitsTitleEverywhere: the other half of the
// same rule — no title anywhere means no title field, as before.
func TestStatuslessKeyWithNoTitleOmitsTitleEverywhere(t *testing.T) {
	evs := []model.Event{ev("lbl0000000", "t1", "labels", "urgent", "alice", "2026-08-17T11:00:00.000")}
	b := Build(contestMeta(), evs)
	env := b.Envelope(mustParseTS("2026-08-17T13:00:00.000"), 0, alwaysTrue)
	for _, a := range env.Attention {
		if a.Title != "" {
			t.Fatalf("an untitled key carries no title: %+v", a)
		}
	}
}
