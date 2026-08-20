package board

import (
	"sort"

	"ledger/internal/dag"
	"ledger/internal/model"
)

// contested: the partition race, fold-derived (sync design, Addition 3).
// One definition, and every other behavior falls out of it:
//
//	For each (key, guarded field) on a ready-capable board, compute the
//	WRITE-HEADS — the writes to that field with no descendant write to the
//	same field. |heads| > 1 is contested, one entry per (key, field).
//
// Consequences worth stating, because each replaced a rule that used to be
// written down separately:
//
//   - The winner is the fold-order-last head, so `expect` is the field's
//     latest event by construction (nothing can follow the fold-order-last
//     write and still be its descendant): a contested entry always carries
//     a VALID CAS ticket, never a stale one.
//   - There are no clearing rules. Any write to the field descends from
//     every current head (the local ref tip does), which collapses the
//     antichain to one — the definition clears itself.
//   - There is NO same-value auto-clear. Two concurrent claims or closes
//     carrying the same value are precisely the duplicate-work disease this
//     exists to flag.
//   - On a linear (never-merged) chain every write descends from the
//     previous one, so there is always exactly one head and nothing is ever
//     contested. Contests are a merged-history phenomenon by construction —
//     but nothing here CONDITIONS on merges: a merge detector would be a
//     second definition of the same fact, and the pass below already costs
//     one linear walk with no set copies on a linear chain.
//
// The machinery, honestly priced (spec rev 6, replacing rev 3-5's n²/8
// ancestor bitsets — 3MB at 5k events but ~312MB at 50k): the write-heads
// question is descendant-EXISTENCE, not general pairwise ancestry. One
// reverse-topological pass computes, per node, the set of guarded
// (key, field) pairs written by its descendants-or-self; a write is a head
// iff no child's set contains its pair; a node's set is freed once its
// parents have consumed it, so peak residency is DAG width × candidate
// pairs — and a sentinel-merge history's width is the replica count.

// TitleField is the pseudo-field the RENAME stream contests under. It is
// not a declared field and can never become one (model.ValidateDeclarations
// reserves the name) — but the write-heads definition is about a stream of
// writes to ONE thing, and a key's renames are exactly that stream. Without
// it, concurrent cross-replica renames would merge in silence while the
// identical status race raised an entry, and `prior` could not tell a race
// loss from a sequential retitle.
const TitleField = model.TitleFieldName

// writesField is the write-heads definition's membership test: does e write
// `field` on its key? An ordinary field write carries it in Fields; a
// rename carries it as the event's own Rename. One definition, both streams.
func writesField(e model.Event, field string) bool {
	if e.Type != "set" {
		return false
	}
	if field == TitleField {
		return e.Rename != ""
	}
	_, ok := e.Fields[field]
	return ok
}

// Contest is one (key, field) antichain of competing write-heads — the
// nested ticket a contested attention entry carries, mirroring `break`'s
// shape rather than flattening new fields into the envelope. Key is which
// key the contest is on; it never rides the wire, because the entry
// carrying this ticket already names the key.
type Contest struct {
	Key   string `json:"-"`
	Field string `json:"field"`
	// IDs are the competing write-heads in fold order, WINNER LAST.
	IDs []string `json:"ids"`
	// Authors is parallel to IDs.
	Authors []string `json:"authors"`
	// Expect is the winner's id: the field's latest event, so the entry is
	// a paste-ready CAS ticket for the collapsing write.
	Expect string `json:"expect"`
	// Human reports whether the key carries the reserved human label, so
	// doctrine can tell the caller they will also need --override.
	Human bool `json:"human"`
}

// ComputeContests runs the board-wide cover-set pass and stores its result
// for Envelope to render — `ready` calls it once, in its fold, against the
// same (events, dag.Result) pair the board was built from. A board that
// never calls it carries no contests at all, which is exactly right for
// every read that has no envelope to put them in.
func (b *Board) ComputeContests(events []model.Event, d dag.Result) {
	b.Contests = nil
	contests := AllContests(b.Meta, events, d)
	if len(contests) == 0 {
		return
	}
	byKey := make(map[string][]Contest, len(contests))
	for _, c := range contests {
		byKey[c.Key] = append(byKey[c.Key], c)
	}
	b.Contests = byKey
}

// AllContests derives every live contest on the board: the board-wide
// cover-set pass, returning contests sorted by (key, field). events must be
// in fold order and d must be the dag.Result that order came from — index
// i of events is node d.Order[i] — which is exactly what store.EventsDAG
// returns; a mismatched pair yields no contests rather than a wrong answer.
//
// Pure in the same sense Build is (a function of meta and the chain), so it
// needs no board: the reserved human label is derived from the same single
// scan that collects the guarded writes, latest write wins, which is
// Build's own rule for the labels multi-field.
func AllContests(meta model.Meta, events []model.Event, d dag.Result) []Contest {
	// Scope: ready-capable boards — their guarded fields, plus the rename
	// stream. The title stream is unguardable, so the old
	// `|| len(meta.Guard) == 0` half of this short-circuit is gone: it would
	// now silence a whole stream it does not bound. (Every minting path —
	// create, import, adopt — refuses a ready-capable board without
	// --guard status, so that half was unreachable in production anyway;
	// this package is pure and takes whatever meta it is handed, so the
	// rule is stated here rather than assumed.)
	if !model.ReadyCapable(meta) {
		return nil
	}
	if len(events) == 0 || len(d.Order) != len(events) {
		return nil
	}

	type pair struct{ key, field string }
	pairID := map[pair]int{}
	var pairs []pair
	var writeCount []int
	evPairs := make([][]int, len(events))
	human := map[string]bool{}
	add := func(i int, key, field string) {
		p := pair{key, field}
		id, seen := pairID[p]
		if !seen {
			id = len(pairs)
			pairID[p] = id
			pairs = append(pairs, p)
			writeCount = append(writeCount, 0)
		}
		writeCount[id]++
		evPairs[i] = append(evPairs[i], id)
	}
	for i, e := range events {
		if e.Type != "set" || e.Key == "" {
			continue
		}
		if v, ok := e.Fields["labels"]; ok {
			human[e.Key] = model.Contains(SplitTokens(v), "human")
		}
		for f := range e.Fields {
			if !model.Contains(meta.Guard, f) {
				continue
			}
			add(i, e.Key, f)
		}
		if writesField(e, TitleField) {
			add(i, e.Key, TitleField)
		}
	}

	// Only a pair written more than once can have more than one head, so the
	// cover sets carry nothing else: this is what keeps residency at DAG
	// width × CANDIDATE pairs on a board whose keys are mostly written once.
	candidate := make([]bool, len(pairs))
	any := false
	for id, n := range writeCount {
		if n > 1 {
			candidate[id] = true
			any = true
		}
	}
	if !any {
		return nil
	}

	heads := coverSetHeads(d, evPairs, candidate, len(pairs))

	out := make([]Contest, 0, len(heads))
	for id, hs := range heads {
		if len(hs) < 2 {
			continue
		}
		c := Contest{Key: pairs[id].key, Field: pairs[id].field, Human: human[pairs[id].key],
			IDs: make([]string, 0, len(hs)), Authors: make([]string, 0, len(hs))}
		for _, i := range hs {
			c.IDs = append(c.IDs, events[i].ID)
			c.Authors = append(c.Authors, events[i].Author)
		}
		c.Expect = c.IDs[len(c.IDs)-1]
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Field < out[j].Field
	})
	return out
}

// coverSetHeads is the pass itself: one walk over the contracted DAG in
// REVERSE fold order (d.Order is a topological order, so reversed it visits
// every child before its parents), accumulating per node the set of
// candidate pair ids written by its descendants-or-self. evPairs[i] is the
// pair ids node i writes; candidate[id] marks the pairs worth tracking.
// Returns, per pair id, its write-heads as fold positions, ascending —
// winner last.
//
// Two things keep this cheap. A node ADOPTS the largest child set it is the
// last consumer of instead of copying it (on a linear chain that is every
// node, so the whole walk performs zero set copies), and every set is freed
// the moment its last parent has consumed it, which is what bounds
// residency to the DAG's width rather than its length.
//
// The result does not depend on the order of any node's child list: the
// accumulator is a union, and adoption only decides WHICH set object the
// union is built in. Two replicas whose git traversals produced different
// adjacency orders therefore render identical entries.
func coverSetHeads(d dag.Result, evPairs [][]int, candidate []bool, numPairs int) [][]int {
	pos := make(map[string]int, len(d.Order))
	for i, sha := range d.Order {
		pos[sha] = i
	}
	// pending[i] counts the parents that have yet to consume i's set.
	pending := make([]int, len(d.Order))
	for _, kids := range d.Children {
		for _, c := range kids {
			if i, ok := pos[c]; ok {
				pending[i]++
			}
		}
	}

	sets := make([]map[int]bool, len(d.Order))
	heads := make([][]int, numPairs)

	for n := len(d.Order) - 1; n >= 0; n-- {
		kids := d.Children[d.Order[n]]

		adopt := -1
		for _, c := range kids {
			ci, ok := pos[c]
			if !ok || pending[ci] != 1 {
				continue // another parent still needs it — copy, don't steal
			}
			if adopt < 0 || len(sets[ci]) > len(sets[adopt]) {
				adopt = ci
			}
		}
		var acc map[int]bool
		if adopt >= 0 {
			acc, sets[adopt], pending[adopt] = sets[adopt], nil, 0
		}
		if acc == nil {
			acc = map[int]bool{}
		}
		for _, c := range kids {
			ci, ok := pos[c]
			if !ok || ci == adopt {
				continue
			}
			for id := range sets[ci] {
				acc[id] = true
			}
			if pending[ci]--; pending[ci] == 0 {
				sets[ci] = nil
			}
		}

		// A write at n is a head iff no DESCENDANT wrote its pair — acc holds
		// exactly the descendants' pairs at this point, before n's own are
		// folded in.
		for _, id := range evPairs[n] {
			if !candidate[id] {
				continue
			}
			if !acc[id] {
				heads[id] = append(heads[id], n)
			}
			acc[id] = true
		}

		if pending[n] == 0 {
			continue // a root: nothing will ever consume its set
		}
		sets[n] = acc
	}

	// The walk collected heads in reverse fold order; the ticket's contract
	// is fold order, winner last.
	for _, hs := range heads {
		for i, j := 0, len(hs)-1; i < j; i, j = i+1, j-1 {
			hs[i], hs[j] = hs[j], hs[i]
		}
	}
	return heads
}

// WriteHeads is the cover-set pass narrowed to ONE (key, field) pair: the
// same reverse-topological walk carrying a single boolean per node instead
// of a set. It returns the pair's write-heads as positions in events (fold
// order, winner last), or nil when the field was never written on the key.
//
// This is the write path's form. A conditional set touches exactly one
// guarded field (issues rule 2) and a rename asserts exactly one title, so
// the `contested_resolved` either records is the heads of that single pair
// — computed from the whole-chain precondition read already in hand, inside
// the CAS retry loop, against each attempt's fresh read. Callers scope it:
// contested covers ready-capable boards only, and this function checks
// nothing (it has no meta to check against); pass TitleField for the rename
// stream.
func WriteHeads(events []model.Event, d dag.Result, key, field string) []int {
	if len(events) == 0 || len(d.Order) != len(events) {
		return nil
	}
	writes := make([]bool, len(events))
	total := 0
	last := -1
	for i, e := range events {
		if e.Key != key || !writesField(e, field) {
			continue
		}
		writes[i] = true
		total++
		last = i
	}
	switch total {
	case 0:
		return nil
	case 1:
		return []int{last} // a lone write is trivially the only head
	}

	pos := make(map[string]int, len(d.Order))
	for i, sha := range d.Order {
		pos[sha] = i
	}
	covered := make([]bool, len(d.Order))
	var heads []int
	for n := len(d.Order) - 1; n >= 0; n-- {
		below := false
		for _, c := range d.Children[d.Order[n]] {
			if ci, ok := pos[c]; ok && covered[ci] {
				below = true
				break
			}
		}
		if writes[n] {
			if !below {
				heads = append(heads, n)
			}
			below = true
		}
		covered[n] = below
	}
	for i, j := 0, len(heads)-1; i < j; i, j = i+1, j-1 {
		heads[i], heads[j] = heads[j], heads[i]
	}
	return heads
}

// ResolvedHeads is what a write to (key, field) would record as
// `contested_resolved`: the losing write-heads' event ids — every head but
// the fold-order-last one, which is the winner and the CAS target the write
// already names. Empty when the field is not contested, which is the
// overwhelmingly common case.
func ResolvedHeads(events []model.Event, d dag.Result, key, field string) []string {
	heads := WriteHeads(events, d, key, field)
	if len(heads) < 2 {
		return nil
	}
	losers := make([]string, 0, len(heads)-1)
	for _, i := range heads[:len(heads)-1] {
		losers = append(losers, events[i].ID)
	}
	return losers
}
