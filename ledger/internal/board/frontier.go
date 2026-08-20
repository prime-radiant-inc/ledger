package board

import (
	"sort"
	"strings"
	"time"

	"ledger/internal/model"
)

// alwaysTrue is a filter matching every key — the equivalent of matchWhere
// with no --where clauses. Production (cmd/ready.go) always builds a real
// matchWhere-backed closure instead; this is a test double standing in for
// "no filtering applies".
func alwaysTrue(*Key) bool { return true }

// ReadyEntry is one pickable key (spec "ready: the board, answered"):
// status=open, not human-labeled, every blocked-by edge terminal.
// UnblockedWithoutEvidence names blockers whose terminal event carries no
// evidence refs — a floor against omission, present only when non-empty.
// Contested marks a key with a live contest (sync design, Addition 3):
// membership is unchanged — a contested key keeps its ordinary list
// placement — but the flag rides along so the race is visible at the point
// of decision. Absent, never false: only a live contest says anything.
type ReadyEntry struct {
	Key                      string      `json:"key"`
	Title                    string      `json:"title"`
	Renamed                  *RenameInfo `json:"renamed,omitempty"`
	Note                     string      `json:"note"`
	TS                       string      `json:"ts"`
	By                       string      `json:"by"`
	ID                       string      `json:"id"`
	Contested                bool        `json:"contested,omitempty"`
	UnblockedWithoutEvidence []string    `json:"unblocked_without_evidence,omitempty"`
}

// WaitingOn is one blocker's classified state, shared by held's
// claimed-but-blocked entries and blocked's own membership: terminal wins
// whenever the blocker's status is terminal (labeled or not); human names
// only a non-terminal human-owned blocker (a human+claimed blocker still
// renders "human" — the accepted flattening); statusless covers both a
// half-seed (a board key with no status write) and an orphan (a name never
// written at all); otherwise the raw non-terminal status value, with
// in-progress further split by staleness.
type WaitingOn struct {
	Key   string `json:"key"`
	State string `json:"state"`
}

// HeldEntry is a spoken-for key: kind "claim" (in-progress, not
// human-labeled) or kind "human" (human-labeled, non-terminal — the label
// dominates status for placement, never information). Status/TS populate
// only on kind "human" (its own base fields); Age/Stale populate whenever
// the key's current status is in-progress — always for a claim entry, and
// additionally for a human entry that is ALSO actively claimed. Stale is a
// pointer so JSON `false` is never dropped by omitempty. WaitingOn appears
// only when the key has at least one unresolved (non-terminal) edge —
// claimed-but-blocked and human-but-blocked are both legal and visible.
type HeldEntry struct {
	Key       string      `json:"key"`
	Title     string      `json:"title"`
	Renamed   *RenameInfo `json:"renamed,omitempty"`
	Kind      string      `json:"kind"`
	Status    string      `json:"status,omitempty"`
	By        string      `json:"by"`
	TS        string      `json:"ts,omitempty"`
	Age       string      `json:"age,omitempty"`
	ID        string      `json:"id"`
	Stale     *bool       `json:"stale,omitempty"`
	Contested bool        `json:"contested,omitempty"`
	WaitingOn []WaitingOn `json:"waiting_on,omitempty"`
}

// BlockedEntry is a waiting key: open, unlabeled, at least one unresolved
// edge. WaitingOn always carries every declared blocker (not just the
// unresolved ones) so a reader sees the full dependency picture in one
// place; id is the status field's latest event — "blocked is not locked"
// is exercisable straight from this id.
type BlockedEntry struct {
	Key       string      `json:"key"`
	Title     string      `json:"title"`
	Renamed   *RenameInfo `json:"renamed,omitempty"`
	Note      string      `json:"note"`
	TS        string      `json:"ts"`
	By        string      `json:"by"`
	ID        string      `json:"id"`
	Contested bool        `json:"contested,omitempty"`
	WaitingOn []WaitingOn `json:"waiting_on"`
}

// AttentionEntry is one triage-queue item. Reason discriminates the shape:
// "stale-claim" carries key/title/by/age/id (human-labeled stale claims
// included); "statusless" carries key (a half-seed or an orphan reference); "cycle" carries Keys (every
// member of the cycle) instead of a singular Key, plus Break, the
// self-service paste-ready fix (spec: "a cycle entry carries its own fix");
// "unknown-status" carries key/value/by/ts/id — a non-human key whose
// status is neither terminal nor one of the two declared non-terminal
// values ({open, in-progress}), the belt-and-braces catch-all for a status
// vocab that was extended out from under board.Build's classification
// switch (see vocab.go's rejection of exactly that on a ready-capable
// board) or folded from an already-polluted or imported chain;
// "contested" carries key/title plus Contest, the nested ticket naming the
// racing write-heads and the CAS target that collapses them. Title (and,
// when the key has been renamed, Renamed) rides EVERY entry whose key has a
// title at all — statusless entries included, since a fold-total rename can
// title a key no status write ever touched; it is omitted only when the key
// has no title from any source.
type AttentionEntry struct {
	Reason  string      `json:"reason"`
	Key     string      `json:"key,omitempty"`
	Title   string      `json:"title,omitempty"`
	Renamed *RenameInfo `json:"renamed,omitempty"`
	Value   string      `json:"value,omitempty"`
	By      string      `json:"by,omitempty"`
	Age     string      `json:"age,omitempty"`
	TS      string      `json:"ts,omitempty"`
	ID      string      `json:"id,omitempty"`
	Keys    []string    `json:"keys,omitempty"`
	Break   *CycleBreak `json:"break,omitempty"`
	Contest *Contest    `json:"contest,omitempty"`
}

// CycleBreak is a "cycle" attention entry's suggested fix: among the
// cycle's members, the one whose blocked-by field's own latest event
// carries the newest timestamp (the youngest edge — the most recently
// added link, and so the least-established one), naming the token to drop
// from that member's blocked-by value. Key is the member to write; Drop is
// the token being removed (the next member in the cycle, the one that edge
// points to); Keep is the resulting full blocked-by value after removing
// EVERY occurrence of Drop, comma-joined, or "" if that empties it (no
// omitempty on Keep or Human — both are meaningful, present-but-zero
// values here, not absent fields, matching the spec's own pinned example
// and HeldEntry.Stale's precedent for never dropping a false/empty value
// via omitempty). Expect is that member's blocked-by field's latest event
// id, the CAS ticket that makes the suggested `ledger set <key>
// blocked-by=<keep> --expect <expect>` a single paste-ready guarded write.
// Human is set when Key itself carries the human label, so doctrine can
// tell the caller to add --override too.
type CycleBreak struct {
	Key    string `json:"key"`
	Drop   string `json:"drop"`
	Keep   string `json:"keep"`
	Expect string `json:"expect"`
	Human  bool   `json:"human"`
}

// Totals carries the true per-list counts, computed before --limit
// truncation — frontier and totals.attention never lie from truncation.
type Totals struct {
	Ready     int `json:"ready"`
	Held      int `json:"held"`
	Blocked   int `json:"blocked"`
	Attention int `json:"attention"`
}

// Envelope is `ready`'s full answer (spec "ready: the board, answered").
// The caller (cmd/ready.go) adds "ledger" and out.Emit adds "ok" — Envelope
// itself only knows the board, not the ledger it came from.
type Envelope struct {
	Frontier  string           `json:"frontier"`
	Ready     []ReadyEntry     `json:"ready"`
	Held      []HeldEntry      `json:"held"`
	Blocked   []BlockedEntry   `json:"blocked"`
	Attention []AttentionEntry `json:"attention"`
	Totals    Totals           `json:"totals"`
}

// Envelope builds the ready/held/blocked/attention lists and the frontier
// verdict. Classification walks the board exactly ONCE, unfiltered
// (buildLists never takes a filter) — filter (a matchWhere-backed closure
// from cmd/ready.go, or a constant-true filter when no filtering applies,
// keeping this package free of a cmd import) is applied afterward, per
// entry, to derive the four displayed lists from that single classified
// result. This reproduces filtering's previous semantics exactly: a
// ready/held/blocked/stale-claim/statusless entry is gated on filter
// applied to its OWN key (filterEntries below looks it up in b.Keys by the
// entry's Key field) — for a statusless entry whose Key names an orphan (no
// set event ever touched it directly, so it's absent from b.Keys),
// b.Keys[name] is the nil *Key, and filter(nil) is exactly matchWhere's
// own nil-key rule (true when there are no clauses, false whenever there
// are, since an orphan carries no field values to test) — no separate rule
// is needed. Cycle entries are the one exception: cycleSurvivesFilter's
// any-member rule (a cycle is relevant to any view containing one of its
// members, not just one where every member does) applies instead, since a
// cycle entry carries no single Key.
//
// Frontier is computed over the FULL, unfiltered board (spec: "computed
// over the FULL board regardless of --limit", read together with "Extra
// --where clauses apply uniformly to all lists" — that sentence names the
// four *lists*, not the verdict — as full in both dimensions: the verdict
// is the board's own truth, not the view asking for it). frontierVerdict
// takes the very same unfiltered ready/attention/workAvailable buildLists
// already computed; the four returned lists are the filtered derivation.
//
// Totals report the filtered, pre-truncation counts (spec: totals are
// honest about what --where left standing, even though --limit still caps
// the returned slice).
func (b *Board) Envelope(now time.Time, limit int, filter func(*Key) bool) Envelope {
	ready, held, blocked, attention, workAvailable := b.buildLists(now)

	frontier := frontierVerdict(ready, attention, workAvailable)

	fReady := filterEntries(b, ready, filter, func(e ReadyEntry) string { return e.Key })
	fHeld := filterEntries(b, held, filter, func(e HeldEntry) string { return e.Key })
	fBlocked := filterEntries(b, blocked, filter, func(e BlockedEntry) string { return e.Key })
	fAttention := filterAttention(b, attention, filter)

	sortReady(b, fReady)
	sort.Slice(fHeld, func(i, j int) bool { return fHeld[i].Key < fHeld[j].Key })
	sort.Slice(fBlocked, func(i, j int) bool { return fBlocked[i].Key < fBlocked[j].Key })
	sortAttention(fAttention)

	totals := Totals{Ready: len(fReady), Held: len(fHeld), Blocked: len(fBlocked), Attention: len(fAttention)}

	return Envelope{
		Frontier:  frontier,
		Ready:     truncate(fReady, limit),
		Held:      truncate(fHeld, limit),
		Blocked:   truncate(fBlocked, limit),
		Attention: truncate(fAttention, limit),
		Totals:    totals,
	}
}

// sortAttention is the attention list's order, complete and TOTAL over
// every entry kind (sync design, Addition 3, which also binds the issues
// spec — it never defined one): entries sort on (sort_key, reason, field),
// where sort_key is the entry's key for keyed entries and the sorted member
// list joined by "," for cycle entries (which carry `keys`, not `key`), and
// field is the empty string wherever there isn't one. Total by
// construction: at most one entry exists per (key, reason) for every reason
// but "contested", which splits by field; cycle entries are deduped by
// their member set, which IS their sort_key. That totality is the point —
// the envelope's byte-determinism must not rest on an implementation's
// incidental sort stability, so this uses a PLAIN (unstable) sort.Slice and
// the order is a property of the entries alone.
//
// Decorate-sort: each entry's sort_key is derived once (a cycle entry's
// costs a copy and a string sort) rather than on every comparison.
func sortAttention(entries []AttentionEntry) {
	type decorated struct {
		entry                  AttentionEntry
		sortKey, reason, field string
	}
	ds := make([]decorated, len(entries))
	for i, e := range entries {
		d := decorated{entry: e, sortKey: e.Key, reason: e.Reason}
		if e.Reason == "cycle" {
			members := append([]string(nil), e.Keys...)
			sort.Strings(members)
			d.sortKey = strings.Join(members, ",")
		}
		if e.Contest != nil {
			d.field = e.Contest.Field
		}
		ds[i] = d
	}
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].sortKey != ds[j].sortKey {
			return ds[i].sortKey < ds[j].sortKey
		}
		if ds[i].reason != ds[j].reason {
			return ds[i].reason < ds[j].reason
		}
		return ds[i].field < ds[j].field
	})
	for i := range ds {
		entries[i] = ds[i].entry
	}
}

// sortReady orders entries by claim TS ascending, tie-broken by the key's
// statusSeq — byte-identical to the naive comparator that calls
// model.ParseTS and looks up statusSeq on every comparison, except each
// entry's TS is parsed and its seq looked up exactly once (decorate-sort)
// instead of up to O(n log n) times over the course of the sort.
func sortReady(b *Board, entries []ReadyEntry) {
	type decorated struct {
		entry ReadyEntry
		ts    time.Time
		seq   int
	}
	ds := make([]decorated, len(entries))
	for i, e := range entries {
		t, _ := model.ParseTS(e.TS)
		ds[i] = decorated{entry: e, ts: t, seq: b.Keys[e.Key].statusSeq}
	}
	sort.Slice(ds, func(i, j int) bool {
		if !ds[i].ts.Equal(ds[j].ts) {
			return ds[i].ts.Before(ds[j].ts)
		}
		return ds[i].seq < ds[j].seq
	})
	for i := range ds {
		entries[i] = ds[i].entry
	}
}

// filterEntries derives the filtered display slice for one entry list: an
// entry survives iff filter(b.Keys[key(entry)]) — its own key exactly as
// matchWhere would evaluate it, nil and all (see Envelope's doc comment).
// Always non-nil, matching buildLists' own empty-slice-not-nil convention.
func filterEntries[T any](b *Board, entries []T, filter func(*Key) bool, key func(T) string) []T {
	out := make([]T, 0, len(entries))
	for _, e := range entries {
		if filter(b.Keys[key(e)]) {
			out = append(out, e)
		}
	}
	return out
}

// filterAttention is filterEntries' attention-specific counterpart: every
// reason but "cycle" filters on its own Key like any other entry; "cycle"
// entries carry no single Key (they name several via Keys) and instead keep
// cycleSurvivesFilter's any-member rule.
func filterAttention(b *Board, entries []AttentionEntry, filter func(*Key) bool) []AttentionEntry {
	out := make([]AttentionEntry, 0, len(entries))
	for _, a := range entries {
		var survives bool
		if a.Reason == "cycle" {
			survives = cycleSurvivesFilter(b, a, filter)
		} else {
			survives = filter(b.Keys[a.Key])
		}
		if survives {
			out = append(out, a)
		}
	}
	return out
}

// buildLists walks the board once, unfiltered, classifying every key into
// ready/held/blocked and appending stale-claim/statusless/cycle attention
// entries (unsorted — Envelope sorts after filtering). workAvailable
// reports whether a reclaimable (non-human-labeled) stale claim was found.
// This is the FULL board's classification: Envelope derives both the
// filtered display lists and the frontier verdict from this single result,
// never walking the board a second time.
func (b *Board) buildLists(now time.Time) (ready []ReadyEntry, held []HeldEntry, blocked []BlockedEntry, attention []AttentionEntry, workAvailable bool) {
	ready = []ReadyEntry{}
	held = []HeldEntry{}
	blocked = []BlockedEntry{}
	attention = []AttentionEntry{}
	attSeen := map[string]bool{}

	names := sortedKeyNames(b.Keys)
	for _, name := range names {
		k := b.Keys[name]
		human := k.HasHuman()
		// stale/age are the one StaleAge(k, now) result this key needs: both
		// heldEntry (below, for an in-progress key) and the stale-claim
		// attention check just after the switch consult the same (key, now)
		// pair, so it's computed once here and threaded to both instead of
		// each re-deriving it.
		var stale bool
		var age time.Duration
		if k.Status != nil && k.Status.Value == "in-progress" {
			stale, age = b.StaleAge(k, now)
		}
		switch {
		case k.Status != nil && k.Status.Value == "open" && !human:
			if terminal, unevidenced := b.allEdgesTerminalUnevidenced(k); terminal {
				ready = append(ready, b.readyEntry(k, unevidenced))
			} else {
				blocked = append(blocked, b.blockedEntry(k, now))
			}
		case k.Status != nil && k.Status.Value == "in-progress" && !human:
			held = append(held, b.heldEntry(k, "claim", now, stale, age))
		case human && k.Status != nil && !b.IsTerminal(k.Status.Value):
			held = append(held, b.heldEntry(k, "human", now, stale, age))
		// Belt-and-braces default arm (finding: a live-extended vocab must
		// never make a key vanish from every list): every case above
		// requires human, "open", or "in-progress" — a non-human key whose
		// non-terminal status is none of those falls through to here
		// instead of disappearing unclassified. The explicit !IsTerminal
		// guard is what keeps an ordinary closed/wontfix key silent, as
		// intended — it never reaches this arm either.
		case k.Status != nil && !human && !b.IsTerminal(k.Status.Value):
			attention = append(attention, AttentionEntry{
				Reason: "unknown-status", Key: k.Name, Value: k.Status.Value,
				By: k.Status.Author, TS: k.Status.TS, ID: k.Status.ID,
			})
			attSeen[k.Name] = true
		}

		if k.Status != nil && k.Status.Value == "in-progress" {
			if stale {
				attention = append(attention, AttentionEntry{
					Reason: "stale-claim", Key: k.Name, Title: k.Title, Renamed: k.RenameInfo(),
					By: k.Status.Author, Age: FormatAge(age), ID: k.Status.ID,
				})
				attSeen[k.Name] = true
				if !human {
					workAvailable = true
				}
			}
		}
		if k.Status == nil {
			attention = append(attention, AttentionEntry{Reason: "statusless", Key: k.Name,
				Title: k.Title, Renamed: k.RenameInfo()})
			attSeen[k.Name] = true
		}
	}

	// Orphans: blocker names no key's own status/labels/blocked-by write
	// ever touched directly, so they carry no *Key at all.
	for _, name := range names {
		k := b.Keys[name]
		for _, blockerName := range k.BlockedBy() {
			if _, exists := b.Keys[blockerName]; exists || attSeen[blockerName] {
				continue
			}
			attention = append(attention, AttentionEntry{Reason: "statusless", Key: blockerName})
			attSeen[blockerName] = true
		}
	}
	for _, cycle := range b.detectCycles(names) {
		attention = append(attention, cycle)
	}
	attention = append(attention, b.contestedEntries()...)

	return ready, held, blocked, attention, workAvailable
}

// contestedEntries renders one attention entry per live contest — per
// (key, field), the rename stream's pseudo-field "title" included, never per
// racing write. Title is the KEY's current title (the latest rename in fold
// order, else the seed message) with its rename label attached, so it is
// absent only on a key with no title at all. A two-root key collision
// genuinely holds one key's title over two seeded tasks, and the losing
// root's seed title appears in no rename structure — doctrine covers that
// hazard (read both heads before collapsing), since a collapse adjudicates
// the contested stream's value, never the identity.
func (b *Board) contestedEntries() []AttentionEntry {
	if len(b.Contests) == 0 {
		return nil
	}
	entries := make([]AttentionEntry, 0, len(b.Contests))
	for _, name := range sortedKeyNames(b.Keys) {
		for i := range b.Contests[name] {
			c := b.Contests[name][i]
			e := AttentionEntry{Reason: "contested", Key: name, Contest: &c}
			if k := b.Keys[name]; k != nil {
				e.Title = k.Title
				e.Renamed = k.RenameInfo()
			}
			entries = append(entries, e)
		}
	}
	return entries
}

// contested reports whether k has a live contest on any GUARDED field — the
// flag every ready/held/blocked entry for that key carries. A title contest
// is deliberately not one of them: it surfaces in attention alone (bridge
// design rev 6), since a cosmetic cross-replica retitle must not mark a
// pickable key as raced.
func (b *Board) contested(k *Key) bool {
	for _, c := range b.Contests[k.Name] {
		if c.Field != TitleField {
			return true
		}
	}
	return false
}

// movesFrontier reports whether an attention entry can move the verdict off
// all-handled. Every entry can except a title contest: holding a whole fleet
// in the picking loop over two replicas' wording is exactly the cost the
// attention-only ruling refuses. The entry is still listed, and
// totals.attention still counts it — a triage cue, not a stop.
func movesFrontier(e AttentionEntry) bool {
	return !(e.Reason == "contested" && e.Contest != nil && e.Contest.Field == TitleField)
}

// cycleSurvivesFilter reports whether any of a cycle attention entry's
// member keys matches filter — the composition rule filterAttention applies
// to "cycle" entries (see the call site).
func cycleSurvivesFilter(b *Board, entry AttentionEntry, filter func(*Key) bool) bool {
	for _, name := range entry.Keys {
		if filter(b.Keys[name]) {
			return true
		}
	}
	return false
}

// frontierVerdict computes the frontier verdict from buildLists' FULL,
// unfiltered ready/attention/workAvailable — the same values Envelope
// itself derives the (filtered) displayed lists from, so the board is only
// ever walked once. work-available: anything pickable now (non-empty ready)
// or reclaimable (a stale claim on a non-human-labeled key); else
// attention-needed: the attention list (stale claims, statusless
// references, cycles, unknown-status keys, guarded-field contests) holds at
// least one entry that moves the verdict — every kind but a title contest,
// which is listed without ever holding the fleet (see movesFrontier); else
// all-handled.
func frontierVerdict(ready []ReadyEntry, attention []AttentionEntry, workAvailable bool) string {
	blocking := false
	for _, a := range attention {
		if movesFrontier(a) {
			blocking = true
			break
		}
	}
	switch {
	case len(ready) > 0 || workAvailable:
		return "work-available"
	case blocking:
		return "attention-needed"
	default:
		// VERIFIED, not a fallback: ready empty and attention empty together
		// mean every key is terminal, a live claim, or a non-terminal human
		// key (all three are valid termini) — anything stale or statusless
		// would be in attention, a non-human key carrying a non-terminal
		// status outside {open, in-progress} would be in attention too (the
		// unknown-status default arm — belt-and-braces against a status
		// vocab extended out from under this switch, e.g. by a polluted or
		// imported chain), and an open+unlabeled key either has every edge
		// resolved (would be in ready) or, in a finite graph, its
		// unresolved walk either reaches a terminus or cycles back on
		// itself, which detectCycles already covers (would be in
		// attention). Depends on ready's membership rule, the stale/
		// statusless/unknown-status attention passes, and detectCycles'
		// coverage all staying as they are — changing any of them re-opens
		// this argument. The contested pass (sync design, Addition 3) only
		// ADDS entries, so it can turn this arm into attention-needed but
		// never makes it wrong: a board whose one unhandled fact is a
		// partition race is exactly a board needing attention.
		return "all-handled"
	}
}

// detectCycles walks the blocked-by graph with path-stack cycle detection
// and a visited memo (spec rule 9's DFS). Holder-blind (trial 5): every
// non-terminal target gets recursed into, regardless of status value or
// labels — a claimed (in-progress) or human-labeled key sits on a cycle
// exactly as validly as an open one, and self-service breaking needs to see
// it. Only a terminal status or a statusless reference (missing key, or a
// key with no status write) ends a chain right there: a terminal status's
// edges are moot, and statusless references are already flagged by
// buildLists' own per-key and orphan passes, so this function neither
// re-flags them nor walks past them. onPath tracks the current recursion
// path (a repeat of a name still onPath is a true cycle — the path slice
// from that name's first occurrence to here names every member, in
// dependency order: path[i] is blocked-by path[i+1], wrapping); finished is
// the shared visited memo that makes diamonds (a node reached twice via
// different paths, but never via itself) legal and never re-walked, so
// shared dependencies cost no more than a single visit each. seen dedupes
// identical-member cycle entries (spec: doubled edges must not double-
// report) by a canonical (sorted) signature of the member set. names is
// the walk's own deterministic starting order — buildLists' own
// sortedKeyNames(b.Keys), passed in rather than recomputed here, since
// it's the identical (b.Keys, ascending) result either way.
func (b *Board) detectCycles(names []string) []AttentionEntry {
	onPath := map[string]bool{}
	finished := map[string]bool{}
	seen := map[string]bool{}
	var path []string
	var entries []AttentionEntry

	var walk func(name string)
	walk = func(name string) {
		if finished[name] {
			return
		}
		k := b.Keys[name]
		if k == nil || k.Status == nil || b.IsTerminal(k.Status.Value) {
			return
		}
		onPath[name] = true
		path = append(path, name)
		for _, blocker := range k.BlockedBy() {
			if onPath[blocker] {
				start := 0
				for i, p := range path {
					if p == blocker {
						start = i
						break
					}
				}
				cycle := append([]string(nil), path[start:]...)
				if sig := cycleSignature(cycle); !seen[sig] {
					seen[sig] = true
					entries = append(entries, b.cycleEntry(cycle))
				}
				continue
			}
			walk(blocker)
		}
		path = path[:len(path)-1]
		onPath[name] = false
		finished[name] = true
	}

	for _, name := range names {
		walk(name)
	}
	return entries
}

// cycleSignature is a cycle's dedup key: its member set, sorted — order-
// and-rotation independent, so two detections of the "same" cycle (e.g. a
// doubled edge triggering the back-edge check twice) collapse to one.
func cycleSignature(cycle []string) string {
	sorted := append([]string(nil), cycle...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// cycleEntry builds a "cycle" attention entry for cycle (detectCycles'
// path-order slice: cycle[i] is blocked-by cycle[i+1], wrapping), including
// the suggested break: the youngest edge in the cycle (per youngerEdge) —
// the member whose blocked-by field's own latest event is the newest —
// dropping every occurrence of the token that points to its successor in
// the cycle.
func (b *Board) cycleEntry(cycle []string) AttentionEntry {
	youngest := 0
	for i := 1; i < len(cycle); i++ {
		if b.youngerEdge(cycle[i], cycle[youngest]) {
			youngest = i
		}
	}

	member := b.Keys[cycle[youngest]]
	next := cycle[(youngest+1)%len(cycle)]
	remaining := make([]string, 0, len(member.BlockedBy()))
	for _, tok := range member.BlockedBy() {
		if tok != next {
			remaining = append(remaining, tok)
		}
	}

	return AttentionEntry{Reason: "cycle", Keys: cycle, Break: &CycleBreak{
		Key: member.Name, Drop: next,
		Keep: strings.Join(remaining, ","), Expect: member.BlockedByID,
		Human: member.HasHuman(),
	}}
}

// youngerEdge reports whether a's blocked-by edge is younger (more
// recently written) than c's. Compares BlockedByTS when both parse and
// differ; otherwise falls back to exact chain order — the member whose
// blocked-by event itself lands later in the event chain (higher
// blockedBySeq) counts as younger. blockedBySeq, not statusSeq, because
// it's the position of the fact actually being compared (the edge write),
// not a proxy borrowed from a different field; IDs are content-addressed
// hashes with no inherent ordering, so chain position is the only
// tiebreaker available. Every member of a detected cycle has a real
// blocked-by edge (reached only through the walk above), so an
// unparseable BlockedByTS should never happen in practice, and an
// exact-timestamp tie is likewise rare — this just gives both a defined,
// deterministic answer rather than leaving the comparison undefined.
func (b *Board) youngerEdge(a, c string) bool {
	ka, kc := b.Keys[a], b.Keys[c]
	ta, errA := model.ParseTS(ka.BlockedByTS)
	tc, errC := model.ParseTS(kc.BlockedByTS)
	if errA == nil && errC == nil && !ta.Equal(tc) {
		return ta.After(tc)
	}
	return ka.blockedBySeq > kc.blockedBySeq
}

// allEdgesTerminal reports whether every one of k's blocked-by edges
// resolves: the blocker exists, has a status, and that status is terminal.
// Vacuously true when k has no edges — ready's and blocked's membership
// split on exactly this.
func (b *Board) allEdgesTerminal(k *Key) bool {
	for _, name := range k.BlockedBy() {
		blocker, exists := b.Keys[name]
		if !exists || blocker.Status == nil || !b.IsTerminal(blocker.Status.Value) {
			return false
		}
	}
	return true
}

// allEdgesTerminalUnevidenced is allEdgesTerminal's ready-path variant: the
// same single walk over k's blocked-by edges also collects unevidenced —
// the terminal blockers whose own terminal event carries no evidence refs
// (readyEntry's UnblockedWithoutEvidence) — so the ready branch (the only
// caller that needs both answers) never walks the same edge set twice.
// unevidenced is only meaningful when terminal is true; on the first
// non-terminal edge this returns immediately, exactly as allEdgesTerminal
// does, since a not-fully-terminal k never reaches readyEntry.
func (b *Board) allEdgesTerminalUnevidenced(k *Key) (terminal bool, unevidenced []string) {
	for _, name := range k.BlockedBy() {
		blocker, exists := b.Keys[name]
		if !exists || blocker.Status == nil || !b.IsTerminal(blocker.Status.Value) {
			return false, nil
		}
		if len(blocker.Status.Evidence) == 0 {
			unevidenced = append(unevidenced, name)
		}
	}
	return true, unevidenced
}

// blockerState classifies one blocker by name for a waiting_on entry (spec
// "blocked": state ∈ terminal | open | in-progress | in-progress-stale |
// human | statusless). Terminal wins whenever the blocker's status is
// terminal, labeled or not; human names only a non-terminal human-owned
// blocker (a human+claimed blocker still renders "human" — the accepted
// flattening); a missing key or a present key with no status write alike
// report statusless.
func (b *Board) blockerState(name string, now time.Time) string {
	blocker, exists := b.Keys[name]
	if !exists || blocker.Status == nil {
		return "statusless"
	}
	if b.IsTerminal(blocker.Status.Value) {
		return "terminal"
	}
	if blocker.HasHuman() {
		return "human"
	}
	if blocker.Status.Value == "in-progress" {
		if stale, _ := b.StaleAge(blocker, now); stale {
			return "in-progress-stale"
		}
		return "in-progress"
	}
	return blocker.Status.Value
}

// waitingOnList classifies every one of k's declared blockers, in
// blocked-by's own order.
func (b *Board) waitingOnList(k *Key, now time.Time) []WaitingOn {
	wo := make([]WaitingOn, 0, len(k.BlockedBy()))
	for _, name := range k.BlockedBy() {
		wo = append(wo, WaitingOn{Key: name, State: b.blockerState(name, now)})
	}
	return wo
}

// readyEntry builds a ready-list entry for k, which the caller has already
// confirmed is open, unlabeled, and fully edge-terminal. id is the claim
// ticket: the status field's own latest event, since status=open IS that
// event here. unblockedWithoutEvidence names every terminal blocker whose
// own terminal event carries no evidence refs, regardless of which terminal
// value it landed — a floor against omission, not a defense against
// fabrication — computed by the caller's own allEdgesTerminalUnevidenced
// walk (the same one that confirmed k is fully edge-terminal) rather than
// re-walked here.
func (b *Board) readyEntry(k *Key, unblockedWithoutEvidence []string) ReadyEntry {
	return ReadyEntry{Key: k.Name, Title: k.Title, Renamed: k.RenameInfo(), Note: k.Status.Note, TS: k.Status.TS, By: k.Status.Author, ID: k.Status.ID,
		Contested: b.contested(k), UnblockedWithoutEvidence: unblockedWithoutEvidence}
}

// blockedEntry builds a blocked-list entry for k, which the caller has
// already confirmed is open, unlabeled, and has at least one unresolved
// edge. id is the status field's own latest event (the open event) — so
// "blocked is not locked" is exercisable straight from this entry.
func (b *Board) blockedEntry(k *Key, now time.Time) BlockedEntry {
	return BlockedEntry{
		Key: k.Name, Title: k.Title, Renamed: k.RenameInfo(), Note: k.Status.Note, TS: k.Status.TS,
		By: k.Status.Author, ID: k.Status.ID, Contested: b.contested(k),
		WaitingOn: b.waitingOnList(k, now),
	}
}

// heldEntry builds a held-list entry for k under the given kind ("claim"
// or "human"). kind "human" additionally carries the base status fields
// (Status, TS) that a claim entry never shows; whenever the key's current
// status is in-progress (always true for a claim entry, and true for a
// human entry that is ALSO actively claimed) it additionally carries the
// claim fields (Age, Stale — By/ID are already the base fields above,
// since status=in-progress's own latest event IS the claim), taken from
// stale/age (the caller's own b.StaleAge(k, now), already computed once
// for this key rather than re-derived here). WaitingOn appears only when k
// has at least one unresolved edge — claimed-but-blocked and human-but-
// blocked are both legal and visible.
func (b *Board) heldEntry(k *Key, kind string, now time.Time, stale bool, age time.Duration) HeldEntry {
	e := HeldEntry{Key: k.Name, Title: k.Title, Renamed: k.RenameInfo(), Kind: kind, By: k.Status.Author, ID: k.Status.ID,
		Contested: b.contested(k)}
	if kind == "human" {
		e.Status = k.Status.Value
		e.TS = k.Status.TS
	}
	if k.Status.Value == "in-progress" {
		e.Age = FormatAge(age)
		e.Stale = &stale
	}
	if !b.allEdgesTerminal(k) {
		e.WaitingOn = b.waitingOnList(k, now)
	}
	return e
}

// sortedKeyNames returns b's key names in ascending order — every list but
// ready sorts key-ascending, and even ready's own pre-sort needs a
// deterministic starting order (map iteration is not stable in Go).
func sortedKeyNames(keys map[string]*Key) []string {
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// truncate caps s at limit entries; limit <= 0 means unlimited (matching
// tail's own --limit convention).
func truncate[T any](s []T, limit int) []T {
	if limit > 0 && len(s) > limit {
		return s[:limit]
	}
	return s
}
