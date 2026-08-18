package board

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"ledger/internal/dag"
	"ledger/internal/model"
)

// contestMeta is the ready-capable, status-guarded shape every contested
// fixture assumes. blocked-by is guarded too, so the two-guarded-fields
// case (one entry per (key, field)) is exercisable.
func contestMeta() model.Meta {
	return model.Meta{
		Fields:      map[string][]string{"status": {"open", "in-progress", "done", "failed"}},
		Terminal:    map[string][]string{"status": {"done", "failed"}},
		Guard:       []string{"status", "blocked-by"},
		MultiFields: []string{"labels", "blocked-by"},
		FieldOrder:  []string{"status"},
	}
}

// dagOf builds a dag.Result over events whose parents are given as fold
// positions, index-aligned with events — the same contract store.EventsDAG
// hands the production code (events in fold order, Children the contracted
// child adjacency). Event ids stand in for commit shas: the pass only ever
// uses them as opaque node names. parents must make the slice order a
// topological order, exactly as dag.Sort's own output is.
func dagOf(events []model.Event, parents [][]int) dag.Result {
	order := make([]string, len(events))
	for i, ev := range events {
		order[i] = ev.ID
	}
	children := map[string][]string{}
	var roots []string
	for i, ps := range parents {
		if len(ps) == 0 {
			roots = append(roots, order[i])
			continue
		}
		for _, p := range ps {
			children[order[p]] = append(children[order[p]], order[i])
		}
	}
	return dag.Result{Order: order, Children: children, Roots: roots}
}

// ev is a compact fixture event: one field write on one key, by one author.
func ev(id, key, field, value, author, ts string) model.Event {
	return model.Event{ID: id, Type: "set", Key: key, Author: author, TS: ts,
		Fields: map[string]string{field: value}}
}

// partitionFixture is the canonical partition shape: a seed both replicas
// saw, then one concurrent write per replica.
//
//	0 seed(open)
//	├── 1 alice
//	└── 2 bob
func partitionFixture(aliceVal, bobVal string) ([]model.Event, dag.Result, model.Meta) {
	evs := []model.Event{
		ev("seed000000", "t1", "status", "open", "alice", "2026-08-17T10:00:00.000"),
		ev("alice00000", "t1", "status", aliceVal, "alice", "2026-08-17T11:00:00.000"),
		ev("bob0000000", "t1", "status", bobVal, "bob", "2026-08-17T12:00:00.000"),
	}
	evs[0].Text = "the task"
	return evs, dagOf(evs, [][]int{{}, {0}, {0}}), contestMeta()
}

// claimThenCloseFixture is the partition run one step further on each side:
// every replica claims and then closes, so the (t1, status) field carries
// FOUR competing writes but only TWO write-heads.
//
//	0 seed(open)
//	├── 1 alice claims ── 3 alice closes
//	└── 2 bob claims   ── 4 bob closes
func claimThenCloseFixture(aliceClose, bobClose string) ([]model.Event, dag.Result, model.Meta) {
	evs := []model.Event{
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		ev("aclaim0000", "t1", "status", "in-progress", "alice", "2026-08-17T11:00:00.000"),
		ev("bclaim0000", "t1", "status", "in-progress", "bob", "2026-08-17T11:30:00.000"),
		ev("aclose0000", "t1", "status", aliceClose, "alice", "2026-08-17T12:00:00.000"),
		ev("bclose0000", "t1", "status", bobClose, "bob", "2026-08-17T12:30:00.000"),
	}
	evs[0].Text = "the task"
	return evs, dagOf(evs, [][]int{{}, {0}, {0}, {1}, {2}}), contestMeta()
}

// contestOn finds the single contest on (key, field), failing if there
// isn't exactly one.
func contestOn(t *testing.T, b *Board, key, field string) Contest {
	t.Helper()
	var found []Contest
	for _, c := range b.Contests[key] {
		if c.Field == field {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one contest on (%s, %s), got %d: %+v", key, field, len(found), b.Contests[key])
	}
	return found[0]
}

// TestConcurrentClaimsFlagOnceWithValidExpect is the core write-heads
// definition: two writes to one guarded field with no descendant write
// between them are an antichain of size 2, flagged ONCE, and the ticket the
// entry hands out is the fold-order-last head — the field's latest event,
// so the CAS it names is valid by construction.
func TestConcurrentClaimsFlagOnceWithValidExpect(t *testing.T) {
	evs, d, meta := partitionFixture("in-progress", "in-progress")
	b := Build(meta, evs)
	b.ComputeContests(evs, d)

	c := contestOn(t, b, "t1", "status")
	if !reflect.DeepEqual(c.IDs, []string{"alice00000", "bob0000000"}) {
		t.Fatalf("ids must be the two heads in fold order, winner last: %v", c.IDs)
	}
	if !reflect.DeepEqual(c.Authors, []string{"alice", "bob"}) {
		t.Fatalf("authors must be parallel to ids: %v", c.Authors)
	}
	if c.Expect != "bob0000000" {
		t.Fatalf("expect = %q, want the fold-order-last head", c.Expect)
	}
	if c.Human {
		t.Fatalf("no human label on this fixture: %+v", c)
	}

	env := b.Envelope(mustParseTS("2026-08-17T13:00:00.000"), 50, alwaysTrue)
	var found []AttentionEntry
	for _, a := range env.Attention {
		if a.Reason == "contested" {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one contested attention entry, got %d: %+v", len(found), env.Attention)
	}
	a := found[0]
	if a.Key != "t1" || a.Title != "the task" || a.Contest == nil || a.Contest.Expect != "bob0000000" {
		t.Fatalf("bad contested entry: %+v", a)
	}
	// Membership is unchanged and visible at the point of decision.
	if len(env.Held) != 1 || !env.Held[0].Contested {
		t.Fatalf("the contested key keeps its held placement and carries the flag: %+v", env.Held)
	}
}

// TestContestedEntryShape pins the entry's exact JSON: reason/key/title plus
// the nested `contest` ticket, and nothing else.
func TestContestedEntryShape(t *testing.T) {
	evs, d, meta := partitionFixture("in-progress", "done")
	b := Build(meta, evs)
	b.ComputeContests(evs, d)

	var entry AttentionEntry
	for _, a := range b.Envelope(mustParseTS("2026-08-17T13:00:00.000"), 50, alwaysTrue).Attention {
		if a.Reason == "contested" {
			entry = a
		}
	}
	got, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"reason":"contested","key":"t1","title":"the task",` +
		`"contest":{"field":"status","ids":["alice00000","bob0000000"],` +
		`"authors":["alice","bob"],"expect":"bob0000000","human":false}}`
	if string(got) != want {
		t.Fatalf("contested entry shape:\n got %s\nwant %s", got, want)
	}
}

// TestContestedEntryOmitsTitleWhenStatusless: the key's title is the first
// status event's message, so a key contested on a NON-status guarded field
// before it ever got a status carries no title at all.
func TestContestedEntryOmitsTitleWhenStatusless(t *testing.T) {
	evs := []model.Event{
		ev("dep0000000", "dep", "status", "open", "seeder", "2026-08-17T09:00:00.000"),
		ev("seed000000", "t1", "blocked-by", "dep", "seeder", "2026-08-17T10:00:00.000"),
		ev("alice00000", "t1", "blocked-by", "", "alice", "2026-08-17T11:00:00.000"),
		ev("bob0000000", "t1", "blocked-by", "dep", "bob", "2026-08-17T12:00:00.000"),
	}
	evs[0].Text = "a dependency"
	d := dagOf(evs, [][]int{{}, {0}, {1}, {1}})
	b := Build(contestMeta(), evs)
	b.ComputeContests(evs, d)

	var entry AttentionEntry
	for _, a := range b.Envelope(mustParseTS("2026-08-17T13:00:00.000"), 50, alwaysTrue).Attention {
		if a.Reason == "contested" {
			entry = a
		}
	}
	if entry.Key != "t1" || entry.Contest == nil || entry.Contest.Field != "blocked-by" {
		t.Fatalf("want a contested entry on (t1, blocked-by): %+v", entry)
	}
	got, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"title"`; contains(string(got), want) {
		t.Fatalf("a statusless key's contested entry must omit title: %s", got)
	}
}

// TestClaimThenCloseFlagsOncePerField: four competing writes to one field,
// two per side, are ONE contest naming the two heads — never one per write,
// never one per side-step.
func TestClaimThenCloseFlagsOncePerField(t *testing.T) {
	evs, d, meta := claimThenCloseFixture("done", "failed")
	b := Build(meta, evs)
	b.ComputeContests(evs, d)

	c := contestOn(t, b, "t1", "status")
	if !reflect.DeepEqual(c.IDs, []string{"aclose0000", "bclose0000"}) {
		t.Fatalf("heads are each side's LATEST write, not its first: %v", c.IDs)
	}
	if c.Expect != "bclose0000" {
		t.Fatalf("expect = %q, want the fold-order-last head", c.Expect)
	}
}

// TestSameValueConcurrentClosesStillFlag pins the rule rev 2 CUT: there is
// NO same-value auto-clear. Two concurrent closes carrying the same value
// are precisely the duplicate-work disease contested exists to flag, and
// they must flag exactly as a differing pair does.
func TestSameValueConcurrentClosesStillFlag(t *testing.T) {
	for _, tc := range [][2]string{{"done", "done"}, {"done", "failed"}} {
		evs, d, meta := claimThenCloseFixture(tc[0], tc[1])
		b := Build(meta, evs)
		b.ComputeContests(evs, d)
		c := contestOn(t, b, "t1", "status")
		if !reflect.DeepEqual(c.IDs, []string{"aclose0000", "bclose0000"}) {
			t.Fatalf("values %v: the values written must not enter the definition: %v", tc, c.IDs)
		}
	}
}

// TestOneEntryPerKeyFieldPair: a key contested on two guarded fields gets
// two entries, one per (key, field), sorted by field.
func TestOneEntryPerKeyFieldPair(t *testing.T) {
	evs := []model.Event{
		ev("dep0000000", "dep", "status", "open", "seeder", "2026-08-17T09:00:00.000"),
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		ev("aedge00000", "t1", "blocked-by", "dep", "alice", "2026-08-17T11:00:00.000"),
		ev("aclaim0000", "t1", "status", "in-progress", "alice", "2026-08-17T11:10:00.000"),
		ev("bedge00000", "t1", "blocked-by", "", "bob", "2026-08-17T12:00:00.000"),
		ev("bclaim0000", "t1", "status", "in-progress", "bob", "2026-08-17T12:10:00.000"),
	}
	evs[0].Text = "a dependency"
	evs[1].Text = "the task"
	d := dagOf(evs, [][]int{{}, {0}, {1}, {2}, {1}, {4}})
	b := Build(contestMeta(), evs)
	b.ComputeContests(evs, d)

	if n := len(b.Contests["t1"]); n != 2 {
		t.Fatalf("want one contest per (key, field), got %d: %+v", n, b.Contests["t1"])
	}
	if b.Contests["t1"][0].Field != "blocked-by" || b.Contests["t1"][1].Field != "status" {
		t.Fatalf("contests on a key sort by field: %+v", b.Contests["t1"])
	}
	env := b.Envelope(mustParseTS("2026-08-17T13:00:00.000"), 50, alwaysTrue)
	var fields []string
	for _, a := range env.Attention {
		if a.Reason == "contested" {
			fields = append(fields, a.Contest.Field)
		}
	}
	if !reflect.DeepEqual(fields, []string{"blocked-by", "status"}) {
		t.Fatalf("two entries, one per field, field-ascending: %v", fields)
	}
}

// TestHumanFlagMirrorsTheKeysLabel: the ticket's `human` is the KEY's label
// state — the same answer Build derives — so doctrine can tell the caller
// the collapsing write also needs --override.
func TestHumanFlagMirrorsTheKeysLabel(t *testing.T) {
	evs, d, meta := partitionFixture("in-progress", "in-progress")
	label := ev("label00000", "t1", "labels", "human,urgent", "alice", "2026-08-17T10:30:00.000")
	evs = append([]model.Event{evs[0], label}, evs[1:]...)
	d = dagOf(evs, [][]int{{}, {0}, {1}, {1}})

	b := Build(meta, evs)
	b.ComputeContests(evs, d)
	c := contestOn(t, b, "t1", "status")
	if !c.Human {
		t.Fatalf("a human-labeled key's contest must carry human:true: %+v", c)
	}
	if c.Human != b.Keys["t1"].HasHuman() {
		t.Fatalf("the ticket's human must be the key's own label state")
	}

	// And it clears with the label, latest write wins.
	unlabel := ev("unlabel000", "t1", "labels", "", "alice", "2026-08-17T12:30:00.000")
	evs = append(evs, unlabel)
	d = dagOf(evs, [][]int{{}, {0}, {1}, {1}, {2}})
	b = Build(meta, evs)
	b.ComputeContests(evs, d)
	c = contestOn(t, b, "t1", "status")
	if c.Human || c.Human != b.Keys["t1"].HasHuman() {
		t.Fatalf("clearing the label clears the ticket's human: %+v", c)
	}
}

// TestCollapsingWriteResolvesAndClears: a write descending from BOTH heads
// collapses the antichain (the definition clears itself, no separate rule),
// and the losing heads are exactly what the collapsing write records.
func TestCollapsingWriteResolvesAndClears(t *testing.T) {
	evs, d, meta := claimThenCloseFixture("done", "done")

	// What the collapsing write would record, computed against the state it
	// sees — before it lands, exactly as setPrecondition does.
	heads := WriteHeads(evs, d, "t1", "status")
	if !reflect.DeepEqual(idsOf(evs, heads), []string{"aclose0000", "bclose0000"}) {
		t.Fatalf("write-path heads must match the board's: %v", idsOf(evs, heads))
	}

	// Now land it: a third write descending from both heads.
	evs = append(evs, ev("collapse00", "t1", "status", "done", "carol", "2026-08-17T13:00:00.000"))
	d = dagOf(evs, [][]int{{}, {0}, {0}, {1}, {2}, {3, 4}})

	b := Build(meta, evs)
	b.ComputeContests(evs, d)
	if len(b.Contests) != 0 {
		t.Fatalf("a write descending from every head must collapse the contest: %+v", b.Contests)
	}
	if h := WriteHeads(evs, d, "t1", "status"); len(h) != 1 || evs[h[0]].ID != "collapse00" {
		t.Fatalf("one head left, the collapsing write: %v", idsOf(evs, h))
	}
}

// TestTouchBaseCollapseResolvesToo: the collapsing write need not know it is
// one — a routine same-value touch-base descending from both heads collapses
// the contest and carries the same losing ids.
func TestTouchBaseCollapseResolvesToo(t *testing.T) {
	evs, d, meta := partitionFixture("in-progress", "in-progress")
	heads := WriteHeads(evs, d, "t1", "status")
	losers := idsOf(evs, heads[:len(heads)-1])
	if !reflect.DeepEqual(losers, []string{"alice00000"}) {
		t.Fatalf("losing heads = every head but the fold-order-last: %v", losers)
	}

	evs = append(evs, ev("touch00000", "t1", "status", "in-progress", "bob", "2026-08-17T13:00:00.000"))
	d = dagOf(evs, [][]int{{}, {0}, {0}, {1, 2}})
	b := Build(meta, evs)
	b.ComputeContests(evs, d)
	if len(b.Contests) != 0 {
		t.Fatalf("a touch-base descending from every head collapses the contest too: %+v", b.Contests)
	}
}

// TestLinearChainNeverContests: on a never-merged chain every write descends
// from the previous one, so the antichain always has exactly one member.
// Contests are a merged-history phenomenon by construction — and a linear
// board's pass produces nothing to render.
func TestLinearChainNeverContests(t *testing.T) {
	evs, _, meta := claimThenCloseFixture("done", "failed")
	d := dagOf(evs, [][]int{{}, {0}, {1}, {2}, {3}}) // one straight line
	b := Build(meta, evs)
	b.ComputeContests(evs, d)
	if len(b.Contests) != 0 {
		t.Fatalf("a linear chain can never be contested: %+v", b.Contests)
	}
	if cs := AllContests(meta, evs, d); cs != nil {
		t.Fatalf("a linear chain's pass yields no contests: %+v", cs)
	}
	env := b.Envelope(mustParseTS("2026-08-17T13:00:00.000"), 50, alwaysTrue)
	for _, a := range env.Attention {
		if a.Reason == "contested" {
			t.Fatalf("no contested entry on a linear board: %+v", a)
		}
	}
}

// TestUnguardedFieldNeverContests pins the stated scope: contested covers
// guarded fields only. An unguarded field's cross-replica race resolves by
// fold order, last write wins, unflagged.
func TestUnguardedFieldNeverContests(t *testing.T) {
	evs, d, meta := partitionFixture("in-progress", "in-progress")
	meta.Guard = nil
	b := Build(meta, evs)
	b.ComputeContests(evs, d)
	if len(b.Contests) != 0 {
		t.Fatalf("no guard, no contest: %+v", b.Contests)
	}
}

// TestPlainBoardNeverContests: a board with no declared terminal status is
// not ready-capable — it has no envelope to carry the flag, so the pass
// never runs there (spec: "Scope, honest").
func TestPlainBoardNeverContests(t *testing.T) {
	evs, d, meta := partitionFixture("in-progress", "in-progress")
	meta.Terminal = nil
	if cs := AllContests(meta, evs, d); cs != nil {
		t.Fatalf("a plain board has no contests: %+v", cs)
	}
}

// TestNotesAndCreatesNeverContest: only "set" events write fields; a note or
// a create sharing the key's name must never enter the antichain.
func TestNotesAndCreatesNeverContest(t *testing.T) {
	evs, d, meta := partitionFixture("in-progress", "in-progress")
	note := model.Event{ID: "note000000", Type: "note", Key: "t1", Author: "carol",
		TS: "2026-08-17T12:30:00.000", Text: "just talking"}
	evs = append(evs, note)
	d = dagOf(evs, [][]int{{}, {0}, {0}, {1, 2}})
	b := Build(meta, evs)
	b.ComputeContests(evs, d)
	c := contestOn(t, b, "t1", "status")
	if !reflect.DeepEqual(c.IDs, []string{"alice00000", "bob0000000"}) {
		t.Fatalf("a note descending from both heads writes no field and collapses nothing: %v", c.IDs)
	}
}

// ---------------------------------------------------------------------
// The cover-set pass against a brute-force reference.
// ---------------------------------------------------------------------

// reachableHeads is the reference implementation of the write-heads
// definition, stated as literally as the spec states it: for each write to
// (key, field), is any OTHER write to the same field reachable from it by
// walking child edges? Quadratic and memo-free on purpose — it exists only
// to hold the cover-set pass honest.
func reachableHeads(events []model.Event, d dag.Result, key, field string) []int {
	pos := map[string]int{}
	for i, sha := range d.Order {
		pos[sha] = i
	}
	writes := map[int]bool{}
	var idxs []int
	for i, e := range events {
		if e.Type == "set" && e.Key == key {
			if _, ok := e.Fields[field]; ok {
				writes[i] = true
				idxs = append(idxs, i)
			}
		}
	}
	var heads []int
	for _, i := range idxs {
		seen := map[int]bool{i: true}
		stack := []int{i}
		isHead := true
		for len(stack) > 0 && isHead {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, c := range d.Children[d.Order[n]] {
				ci := pos[c]
				if seen[ci] {
					continue
				}
				if writes[ci] {
					isHead = false
					break
				}
				seen[ci] = true
				stack = append(stack, ci)
			}
		}
		if isHead {
			heads = append(heads, i)
		}
	}
	return heads
}

// coverSetFixtures are the shapes the two implementations are compared on:
// linear, the canonical partition, claim-then-close, a collapse, a diamond
// (a node reached twice via different paths but never via itself), a
// three-way partition, and a partition whose sides re-merge only partially.
func coverSetFixtures() []struct {
	name    string
	events  []model.Event
	dag     dag.Result
	meta    model.Meta
	pairs   [][2]string
	numHead map[string]int
} {
	type fixture = struct {
		name    string
		events  []model.Event
		dag     dag.Result
		meta    model.Meta
		pairs   [][2]string
		numHead map[string]int
	}
	var out []fixture

	linEvs, _, meta := claimThenCloseFixture("done", "failed")
	out = append(out, fixture{name: "linear", events: linEvs,
		dag:   dagOf(linEvs, [][]int{{}, {0}, {1}, {2}, {3}}),
		meta:  meta,
		pairs: [][2]string{{"t1", "status"}}})

	pEvs, pDag, pMeta := partitionFixture("in-progress", "done")
	out = append(out, fixture{name: "partition", events: pEvs, dag: pDag, meta: pMeta,
		pairs: [][2]string{{"t1", "status"}}})

	cEvs, cDag, cMeta := claimThenCloseFixture("done", "failed")
	out = append(out, fixture{name: "claim-then-close", events: cEvs, dag: cDag, meta: cMeta,
		pairs: [][2]string{{"t1", "status"}}})

	colEvs := append(append([]model.Event{}, cEvs...),
		ev("collapse00", "t1", "status", "done", "carol", "2026-08-17T13:00:00.000"))
	out = append(out, fixture{name: "collapsed", events: colEvs,
		dag:   dagOf(colEvs, [][]int{{}, {0}, {0}, {1}, {2}, {3, 4}}),
		meta:  cMeta,
		pairs: [][2]string{{"t1", "status"}}})

	// Diamond: two branches that write NOTHING to the field re-merge, then
	// one write lands on the merge. Exactly one head, reached twice.
	diaEvs := []model.Event{
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		ev("left000000", "t2", "status", "open", "alice", "2026-08-17T11:00:00.000"),
		ev("right00000", "t3", "status", "open", "bob", "2026-08-17T11:30:00.000"),
		ev("merge00000", "t1", "status", "done", "carol", "2026-08-17T12:00:00.000"),
	}
	diaEvs[0].Text, diaEvs[1].Text, diaEvs[2].Text = "one", "two", "three"
	out = append(out, fixture{name: "diamond", events: diaEvs,
		dag:   dagOf(diaEvs, [][]int{{}, {0}, {0}, {1, 2}}),
		meta:  cMeta,
		pairs: [][2]string{{"t1", "status"}, {"t2", "status"}, {"t3", "status"}}})

	// Three-way partition: three replicas each claim off the same seed.
	triEvs := []model.Event{
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		ev("a000000000", "t1", "status", "in-progress", "alice", "2026-08-17T11:00:00.000"),
		ev("b000000000", "t1", "status", "in-progress", "bob", "2026-08-17T11:30:00.000"),
		ev("c000000000", "t1", "status", "done", "carol", "2026-08-17T12:00:00.000"),
	}
	triEvs[0].Text = "the task"
	out = append(out, fixture{name: "three-way", events: triEvs,
		dag:   dagOf(triEvs, [][]int{{}, {0}, {0}, {0}}),
		meta:  cMeta,
		pairs: [][2]string{{"t1", "status"}}})

	// Partial re-merge: alice and bob diverge, a third replica merges only
	// alice's side and writes again — bob's head survives, alice's does not.
	parEvs := []model.Event{
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		ev("alice00000", "t1", "status", "in-progress", "alice", "2026-08-17T11:00:00.000"),
		ev("bob0000000", "t1", "status", "in-progress", "bob", "2026-08-17T11:30:00.000"),
		ev("dave000000", "t1", "status", "done", "dave", "2026-08-17T12:00:00.000"),
	}
	parEvs[0].Text = "the task"
	out = append(out, fixture{name: "partial-remerge", events: parEvs,
		dag:   dagOf(parEvs, [][]int{{}, {0}, {0}, {1}}),
		meta:  cMeta,
		pairs: [][2]string{{"t1", "status"}}})

	// Two guarded fields racing on one key.
	twoEvs := []model.Event{
		ev("dep0000000", "dep", "status", "open", "seeder", "2026-08-17T09:00:00.000"),
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		ev("aedge00000", "t1", "blocked-by", "dep", "alice", "2026-08-17T11:00:00.000"),
		ev("aclaim0000", "t1", "status", "in-progress", "alice", "2026-08-17T11:10:00.000"),
		ev("bedge00000", "t1", "blocked-by", "", "bob", "2026-08-17T12:00:00.000"),
		ev("bclaim0000", "t1", "status", "in-progress", "bob", "2026-08-17T12:10:00.000"),
	}
	twoEvs[0].Text, twoEvs[1].Text = "a dependency", "the task"
	out = append(out, fixture{name: "two-fields", events: twoEvs,
		dag:   dagOf(twoEvs, [][]int{{}, {0}, {1}, {2}, {1}, {4}}),
		meta:  cMeta,
		pairs: [][2]string{{"t1", "status"}, {"t1", "blocked-by"}, {"dep", "status"}}})

	return out
}

// TestCoverSetHeadsMatchReachabilityReference is the algorithm's correctness
// proof: the reverse-topological cover-set pass (both the board-wide form
// and the write path's single-pair form) must agree with a brute-force
// pairwise-reachability reference on every fixture shape.
func TestCoverSetHeadsMatchReachabilityReference(t *testing.T) {
	for _, f := range coverSetFixtures() {
		t.Run(f.name, func(t *testing.T) {
			b := Build(f.meta, f.events)
			b.ComputeContests(f.events, f.dag)
			for _, p := range f.pairs {
				key, field := p[0], p[1]
				want := reachableHeads(f.events, f.dag, key, field)
				if got := WriteHeads(f.events, f.dag, key, field); !reflect.DeepEqual(got, want) {
					t.Fatalf("(%s, %s): WriteHeads = %v, reference = %v", key, field, idsOf(f.events, got), idsOf(f.events, want))
				}
				// The board-wide pass agrees too: a contest exists iff the
				// reference found more than one head, and names the same ids.
				var wantIDs []string
				if len(want) > 1 {
					wantIDs = idsOf(f.events, want)
				}
				var gotIDs []string
				for _, c := range b.Contests[key] {
					if c.Field == field {
						gotIDs = c.IDs
					}
				}
				if !reflect.DeepEqual(gotIDs, wantIDs) {
					t.Fatalf("(%s, %s): AllContests ids = %v, reference = %v", key, field, gotIDs, wantIDs)
				}
			}
		})
	}
}

// TestContestsIndependentOfChildAdjacencyOrder: the pass unions descendant
// sets, so permuting each node's child list — the one thing about a
// dag.Result that a replica's own git traversal can legitimately vary —
// must not move a single byte of the result.
func TestContestsIndependentOfChildAdjacencyOrder(t *testing.T) {
	for _, f := range coverSetFixtures() {
		t.Run(f.name, func(t *testing.T) {
			b := Build(f.meta, f.events)
			b.ComputeContests(f.events, f.dag)
			want, err := json.Marshal(b.Envelope(mustParseTS("2026-08-17T20:00:00.000"), 50, alwaysTrue).Attention)
			if err != nil {
				t.Fatal(err)
			}
			reversed := dag.Result{Order: f.dag.Order, Roots: f.dag.Roots, Children: map[string][]string{}}
			for p, cs := range f.dag.Children {
				rev := make([]string, len(cs))
				for i := range cs {
					rev[i] = cs[len(cs)-1-i]
				}
				reversed.Children[p] = rev
			}
			b2 := Build(f.meta, f.events)
			b2.ComputeContests(f.events, reversed)
			got, err := json.Marshal(b2.Envelope(mustParseTS("2026-08-17T20:00:00.000"), 50, alwaysTrue).Attention)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("child-order permutation changed the entries:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// TestContestsByteIdenticalAcrossMergeOrders is the replica-convergence
// property in miniature: two replicas that merged the SAME chain in
// different orders hold different commit graphs — different sentinels,
// different merge-parent order — but the sentinels are contracted out
// before this pass ever sees the DAG, so the entries they render must be
// byte-identical.
//
// Replica A merged bob's side into alice's; replica B merged alice's into
// bob's, and then each side wrote once more on top of its own merge before
// the two converged. The contracted DAGs are the same graph; only the
// adjacency ORDER each replica's traversal produced differs.
func TestContestsByteIdenticalAcrossMergeOrders(t *testing.T) {
	evs := []model.Event{
		ev("seed000000", "t1", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		ev("seed222222", "t2", "status", "open", "seeder", "2026-08-17T10:10:00.000"),
		ev("alice00000", "t1", "status", "in-progress", "alice", "2026-08-17T11:00:00.000"),
		ev("bob0000000", "t1", "status", "in-progress", "bob", "2026-08-17T11:30:00.000"),
		ev("alice22222", "t2", "status", "in-progress", "alice", "2026-08-17T12:00:00.000"),
		ev("bob2222222", "t2", "status", "in-progress", "bob", "2026-08-17T12:30:00.000"),
	}
	evs[0].Text, evs[1].Text = "task one", "task two"
	parents := [][]int{{}, {0}, {1}, {1}, {2}, {3}}

	// Replica A: children listed in fetch order. Replica B: the same
	// contracted graph, every adjacency list built the other way round, and
	// the roots reported in the other order.
	a := dagOf(evs, parents)
	b := dag.Result{Order: a.Order, Children: map[string][]string{}}
	for p, cs := range a.Children {
		rev := make([]string, len(cs))
		for i := range cs {
			rev[i] = cs[len(cs)-1-i]
		}
		b.Children[p] = rev
	}
	for i := len(a.Roots) - 1; i >= 0; i-- {
		b.Roots = append(b.Roots, a.Roots[i])
	}

	render := func(d dag.Result) string {
		bd := Build(contestMeta(), evs)
		bd.ComputeContests(evs, d)
		out, err := json.Marshal(bd.Envelope(mustParseTS("2026-08-17T20:00:00.000"), 50, alwaysTrue))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if ra, rb := render(a), render(b); ra != rb {
		t.Fatalf("replicas that merged in different orders must render identically:\nA: %s\nB: %s", ra, rb)
	}
}

// ---------------------------------------------------------------------
// The attention sort: total over every entry kind.
// ---------------------------------------------------------------------

// TestAttentionSortIsTotalOverEveryReason builds one envelope carrying two
// cycles, two contested entries (same key, different fields), a stale claim
// and a statusless reference, and pins the resulting order: entries sort on
// (sort_key, reason, field), where sort_key is the key for keyed entries
// and the sorted member list joined by "," for cycle entries. Every
// permutation of the input must produce the identical order — the envelope's
// byte-determinism must not rest on an implementation's incidental sort
// stability, so this asserts the order is TOTAL, not merely stable.
func TestAttentionSortIsTotalOverEveryReason(t *testing.T) {
	meta := contestMeta()
	meta.StaleAfter = "1h"
	evs := []model.Event{
		// Cycle one: c-a <-> c-b.
		ev("ca000000000", "c-a", "status", "open", "seeder", "2026-08-17T10:00:00.000"),
		ev("cb000000000", "c-b", "status", "open", "seeder", "2026-08-17T10:01:00.000"),
		ev("cae0000000", "c-a", "blocked-by", "c-b", "seeder", "2026-08-17T10:02:00.000"),
		ev("cbe0000000", "c-b", "blocked-by", "c-a", "seeder", "2026-08-17T10:03:00.000"),
		// Cycle two: d-a <-> d-b.
		ev("da000000000", "d-a", "status", "open", "seeder", "2026-08-17T10:04:00.000"),
		ev("db000000000", "d-b", "status", "open", "seeder", "2026-08-17T10:05:00.000"),
		ev("dae0000000", "d-a", "blocked-by", "d-b", "seeder", "2026-08-17T10:06:00.000"),
		ev("dbe0000000", "d-b", "blocked-by", "d-a", "seeder", "2026-08-17T10:07:00.000"),
		// A stale claim.
		ev("st000000000", "m-stale", "status", "open", "seeder", "2026-08-17T10:08:00.000"),
		ev("stc0000000", "m-stale", "status", "in-progress", "worker", "2026-08-17T10:09:00.000"),
		// A statusless half-seed.
		ev("hs000000000", "z-halfseed", "labels", "urgent", "seeder", "2026-08-17T10:10:00.000"),
		// A key contested on both guarded fields.
		ev("xs000000000", "m-contest", "status", "open", "seeder", "2026-08-17T10:11:00.000"),
		ev("xa000000000", "m-contest", "status", "in-progress", "alice", "2026-08-17T11:00:00.000"),
		ev("xae0000000", "m-contest", "blocked-by", "c-a", "alice", "2026-08-17T11:01:00.000"),
		ev("xb000000000", "m-contest", "status", "in-progress", "bob", "2026-08-17T11:02:00.000"),
		ev("xbe0000000", "m-contest", "blocked-by", "d-a", "bob", "2026-08-17T11:03:00.000"),
	}
	evs[0].Text, evs[1].Text = "cycle a", "cycle b"
	evs[4].Text, evs[5].Text = "cycle d-a", "cycle d-b"
	evs[8].Text, evs[11].Text = "stale one", "contested one"
	// Linear up to the contest, then alice's and bob's sides diverge from
	// m-contest's seed (index 11).
	parents := make([][]int, len(evs))
	for i := 1; i <= 12; i++ {
		parents[i] = []int{i - 1}
	}
	parents[13] = []int{12}
	parents[14] = []int{11}
	parents[15] = []int{14}

	b := Build(meta, evs)
	b.ComputeContests(evs, dagOf(evs, parents))
	env := b.Envelope(mustParseTS("2026-08-17T20:00:00.000"), 50, alwaysTrue)

	var got []string
	for _, a := range env.Attention {
		field := ""
		if a.Contest != nil {
			field = a.Contest.Field
		}
		got = append(got, fmt.Sprintf("%s|%s|%s", sortKeyOfEntry(a), a.Reason, field))
	}
	// m-contest is claimed and long past the stale horizon as well as
	// contested on both fields, so it carries THREE entries sharing one
	// sort_key — the case that makes (sort_key, reason, field) load-bearing.
	want := []string{
		"c-a,c-b|cycle|",
		"d-a,d-b|cycle|",
		"m-contest|contested|blocked-by",
		"m-contest|contested|status",
		"m-contest|stale-claim|",
		"m-stale|stale-claim|",
		"z-halfseed|statusless|",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attention order:\n got %v\nwant %v", got, want)
	}

	// Totality: no two entries compare equal, so any input permutation lands
	// the same order under a plain (unstable) sort.
	seen := map[string]bool{}
	for _, k := range got {
		if seen[k] {
			t.Fatalf("two attention entries share a sort key (%s) — the order is not total", k)
		}
		seen[k] = true
	}
	shuffled := append([]AttentionEntry(nil), env.Attention...)
	for i := range shuffled {
		j := len(shuffled) - 1 - i
		if j <= i {
			break
		}
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	sortAttention(shuffled)
	if !reflect.DeepEqual(shuffled, env.Attention) {
		t.Fatalf("a reversed input must sort back to the identical order:\n got %+v\nwant %+v", shuffled, env.Attention)
	}
}

// sortKeyOfEntry is the test's own reading of the spec's sort_key rule,
// written independently of the implementation it checks.
func sortKeyOfEntry(a AttentionEntry) string {
	if a.Reason == "cycle" {
		members := append([]string(nil), a.Keys...)
		sort.Strings(members)
		return join(members, ",")
	}
	return a.Key
}

func join(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}

func idsOf(events []model.Event, idxs []int) []string {
	if idxs == nil {
		return nil
	}
	out := make([]string, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, events[i].ID)
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
