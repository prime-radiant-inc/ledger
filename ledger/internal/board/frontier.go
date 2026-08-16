package board

import (
	"sort"
	"time"

	"ledger/internal/model"
)

// alwaysTrue is the always-true filter frontierVerdict uses to walk the
// FULL board regardless of the caller's --where filter — see Envelope's own
// doc comment.
func alwaysTrue(*Key) bool { return true }

// ReadyEntry is one pickable key (spec "ready: the board, answered"):
// status=open, not human-labeled, every blocked-by edge terminal.
// UnblockedWithoutEvidence names blockers whose terminal event carries no
// evidence refs — a floor against omission, present only when non-empty.
type ReadyEntry struct {
	Key                      string   `json:"key"`
	Title                    string   `json:"title"`
	Note                     string   `json:"note"`
	TS                       string   `json:"ts"`
	By                       string   `json:"by"`
	ID                       string   `json:"id"`
	UnblockedWithoutEvidence []string `json:"unblocked_without_evidence,omitempty"`
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
	Kind      string      `json:"kind"`
	Status    string      `json:"status,omitempty"`
	By        string      `json:"by"`
	TS        string      `json:"ts,omitempty"`
	Age       string      `json:"age,omitempty"`
	ID        string      `json:"id"`
	Stale     *bool       `json:"stale,omitempty"`
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
	Note      string      `json:"note"`
	TS        string      `json:"ts"`
	By        string      `json:"by"`
	ID        string      `json:"id"`
	WaitingOn []WaitingOn `json:"waiting_on"`
}

// AttentionEntry is one triage-queue item. Reason discriminates the shape:
// "stale-claim" carries key/title/by/age/id (human-labeled stale claims
// included — title appears on this reason only); "statusless" carries only
// key (a half-seed or an orphan reference); "cycle" carries Keys (every
// member of the cycle) instead of a singular Key.
type AttentionEntry struct {
	Reason string   `json:"reason"`
	Key    string   `json:"key,omitempty"`
	Title  string   `json:"title,omitempty"`
	By     string   `json:"by,omitempty"`
	Age    string   `json:"age,omitempty"`
	ID     string   `json:"id,omitempty"`
	Keys   []string `json:"keys,omitempty"`
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
// verdict. filter is applied to every real key (board.Build's board.Keys)
// exactly as show --where does — pass matchWhere-backed closure from
// cmd/ready.go, or a constant-true filter when no filtering applies, to
// keep this package free of a cmd import. filter(nil) is also consulted
// once to decide whether orphan blocker references (a name no set event
// ever touched) surface as statusless attention: matchWhere's own nil-key
// rule already gives the right answer (true when there are no clauses,
// false whenever there are, since an orphan carries no field values to
// test) so no separate rule is needed here.
//
// Frontier is computed over the FULL, unfiltered board (spec: "computed
// over the FULL board regardless of --limit", read together with "Extra
// --where clauses apply uniformly to all lists" — that sentence names the
// four *lists*, not the verdict — as full in both dimensions: the verdict
// is the board's own truth, not the view asking for it). frontierVerdict
// re-walks the board under an always-true filter to get there; the four
// returned lists still honor the caller's real filter.
func (b *Board) Envelope(now time.Time, limit int, filter func(*Key) bool) Envelope {
	ready, held, blocked, attention, _ := b.buildLists(now, filter)

	sort.Slice(ready, func(i, j int) bool {
		ti, _ := model.ParseTS(ready[i].TS)
		tj, _ := model.ParseTS(ready[j].TS)
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return b.Keys[ready[i].Key].statusSeq < b.Keys[ready[j].Key].statusSeq
	})
	sort.Slice(held, func(i, j int) bool { return held[i].Key < held[j].Key })
	sort.Slice(blocked, func(i, j int) bool { return blocked[i].Key < blocked[j].Key })
	sort.Slice(attention, func(i, j int) bool { return attention[i].Key < attention[j].Key })

	totals := Totals{Ready: len(ready), Held: len(held), Blocked: len(blocked), Attention: len(attention)}

	return Envelope{
		Frontier:  b.frontierVerdict(now),
		Ready:     truncate(ready, limit),
		Held:      truncate(held, limit),
		Blocked:   truncate(blocked, limit),
		Attention: truncate(attention, limit),
		Totals:    totals,
	}
}

// buildLists walks the board once under filter, classifying every matching
// key into ready/held/blocked and appending stale-claim/statusless/cycle
// attention entries (unsorted — Envelope sorts after this returns).
// workAvailable reports whether a reclaimable (non-human-labeled) stale
// claim was found. Envelope calls this under the caller's real filter for
// the four displayed lists; frontierVerdict calls it again under alwaysTrue
// for the verdict's full-board truth.
func (b *Board) buildLists(now time.Time, filter func(*Key) bool) (ready []ReadyEntry, held []HeldEntry, blocked []BlockedEntry, attention []AttentionEntry, workAvailable bool) {
	ready = []ReadyEntry{}
	held = []HeldEntry{}
	blocked = []BlockedEntry{}
	attention = []AttentionEntry{}
	attSeen := map[string]bool{}

	names := sortedKeyNames(b.Keys)
	for _, name := range names {
		k := b.Keys[name]
		if !filter(k) {
			continue
		}
		human := k.HasHuman()
		switch {
		case k.Status != nil && k.Status.Value == "open" && !human:
			if b.allEdgesTerminal(k) {
				ready = append(ready, b.readyEntry(k))
			} else {
				blocked = append(blocked, b.blockedEntry(k, now))
			}
		case k.Status != nil && k.Status.Value == "in-progress" && !human:
			held = append(held, b.heldEntry(k, "claim", now))
		case human && k.Status != nil && !b.IsTerminal(k.Status.Value):
			held = append(held, b.heldEntry(k, "human", now))
		}

		if k.Status != nil && k.Status.Value == "in-progress" {
			if stale, age := b.StaleAge(k, now); stale {
				attention = append(attention, AttentionEntry{
					Reason: "stale-claim", Key: k.Name, Title: k.Title,
					By: k.Status.Author, Age: FormatAge(age), ID: k.Status.ID,
				})
				attSeen[k.Name] = true
				if !human {
					workAvailable = true
				}
			}
		}
		if k.Status == nil {
			attention = append(attention, AttentionEntry{Reason: "statusless", Key: k.Name})
			attSeen[k.Name] = true
		}
	}

	// Orphans: blocker names no key's own status/labels/blocked-by write
	// ever touched directly, so they carry no *Key at all. filter(nil) is
	// loop-invariant (matchWhere ignores the key's identity when it's nil,
	// only the presence of clauses matters), so compute it once.
	if filter(nil) {
		for _, name := range names {
			k := b.Keys[name]
			if !filter(k) {
				continue
			}
			for _, blockerName := range k.BlockedBy {
				if _, exists := b.Keys[blockerName]; exists || attSeen[blockerName] {
					continue
				}
				attention = append(attention, AttentionEntry{Reason: "statusless", Key: blockerName})
				attSeen[blockerName] = true
			}
		}
	}
	// Cycle entries compose with filter differently from every other
	// attention reason: a stale-claim or statusless entry is gated on its
	// own single key matching, but a cycle names several keys at once, and
	// the controller's ruling is that the entry survives iff ANY member
	// matches — the cycle is relevant to any view containing one of its
	// members, not just one where every member does.
	for _, cycle := range b.detectCycles() {
		if cycleSurvivesFilter(b, cycle, filter) {
			attention = append(attention, cycle)
		}
	}

	return ready, held, blocked, attention, workAvailable
}

// cycleSurvivesFilter reports whether any of a cycle attention entry's
// member keys matches filter — the composition rule buildLists applies to
// "cycle" entries (see the call site).
func cycleSurvivesFilter(b *Board, entry AttentionEntry, filter func(*Key) bool) bool {
	for _, name := range entry.Keys {
		if filter(b.Keys[name]) {
			return true
		}
	}
	return false
}

// frontierVerdict computes the frontier verdict over the FULL, unfiltered
// board — buildLists under alwaysTrue gives the same ready/attention/
// workAvailable classification the displayed lists use, just untouched by
// the caller's --where filter or --limit (buildLists never truncates).
// work-available: anything pickable now (non-empty ready) or reclaimable (a
// stale claim on a non-human-labeled key); else attention-needed: the
// attention list (stale claims, statusless references, cycles) is
// non-empty; else all-handled.
func (b *Board) frontierVerdict(now time.Time) string {
	ready, _, _, attention, workAvailable := b.buildLists(now, alwaysTrue)
	switch {
	case len(ready) > 0 || workAvailable:
		return "work-available"
	case len(attention) > 0:
		return "attention-needed"
	default:
		// VERIFIED, not a fallback: ready empty and attention empty together
		// mean every key is terminal, a live claim, or a non-terminal human
		// key (all three are valid termini) — anything stale or statusless
		// would be in attention, and an open+unlabeled key either has every
		// edge resolved (would be in ready) or, in a finite graph, its
		// unresolved walk either reaches a terminus or cycles back on
		// itself, which detectCycles already covers (would be in
		// attention). Depends on ready's membership rule, the stale/
		// statusless attention passes, and detectCycles' coverage all
		// staying as they are — changing any of them re-opens this
		// argument.
		return "all-handled"
	}
}

// detectCycles walks the blocked-by graph with path-stack cycle detection
// and a visited memo (spec rule 9's DFS). Only a target that is currently
// open and NOT human-labeled gets recursed into ("open targets recursed") —
// a terminal status, a live claim, a non-terminal human-owned key, or a
// statusless reference (missing key, or a key with no status write) all end
// a chain right there: the first three are the spec's valid termini, and
// statusless references are already flagged by buildLists' own per-key and
// orphan passes, so this function neither re-flags them nor walks past
// them. onPath tracks the current recursion path (a repeat of a name still
// onPath is a true cycle — the path slice from that name's first occurrence
// to here names every member); finished is the shared visited memo that
// makes diamonds (a node reached twice via different paths, but never via
// itself) legal and never re-walked, so shared dependencies cost no more
// than a single visit each.
func (b *Board) detectCycles() []AttentionEntry {
	onPath := map[string]bool{}
	finished := map[string]bool{}
	var path []string
	var entries []AttentionEntry

	var walk func(name string)
	walk = func(name string) {
		if finished[name] {
			return
		}
		k := b.Keys[name]
		if k == nil || k.Status == nil || k.Status.Value != "open" || k.HasHuman() {
			return
		}
		onPath[name] = true
		path = append(path, name)
		for _, blocker := range k.BlockedBy {
			if onPath[blocker] {
				start := 0
				for i, p := range path {
					if p == blocker {
						start = i
						break
					}
				}
				cycle := append([]string(nil), path[start:]...)
				entries = append(entries, AttentionEntry{Reason: "cycle", Keys: cycle})
				continue
			}
			walk(blocker)
		}
		path = path[:len(path)-1]
		onPath[name] = false
		finished[name] = true
	}

	for _, name := range sortedKeyNames(b.Keys) {
		walk(name)
	}
	return entries
}

// allEdgesTerminal reports whether every one of k's blocked-by edges
// resolves: the blocker exists, has a status, and that status is terminal.
// Vacuously true when k has no edges — ready's and blocked's membership
// split on exactly this.
func (b *Board) allEdgesTerminal(k *Key) bool {
	for _, name := range k.BlockedBy {
		blocker, exists := b.Keys[name]
		if !exists || blocker.Status == nil || !b.IsTerminal(blocker.Status.Value) {
			return false
		}
	}
	return true
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
	wo := make([]WaitingOn, 0, len(k.BlockedBy))
	for _, name := range k.BlockedBy {
		wo = append(wo, WaitingOn{Key: name, State: b.blockerState(name, now)})
	}
	return wo
}

// readyEntry builds a ready-list entry for k, which the caller has already
// confirmed is open, unlabeled, and fully edge-terminal. id is the claim
// ticket: the status field's own latest event, since status=open IS that
// event here. unblocked_without_evidence names every terminal blocker
// whose own terminal event carries no evidence refs, regardless of which
// terminal value it landed — a floor against omission, not a defense
// against fabrication.
func (b *Board) readyEntry(k *Key) ReadyEntry {
	e := ReadyEntry{Key: k.Name, Title: k.Title, Note: k.Status.Note, TS: k.Status.TS, By: k.Status.Author, ID: k.Status.ID}
	for _, name := range k.BlockedBy {
		blocker := b.Keys[name] // exists and terminal — allEdgesTerminal already verified this
		if len(blocker.Status.Evidence) == 0 {
			e.UnblockedWithoutEvidence = append(e.UnblockedWithoutEvidence, name)
		}
	}
	return e
}

// blockedEntry builds a blocked-list entry for k, which the caller has
// already confirmed is open, unlabeled, and has at least one unresolved
// edge. id is the status field's own latest event (the open event) — so
// "blocked is not locked" is exercisable straight from this entry.
func (b *Board) blockedEntry(k *Key, now time.Time) BlockedEntry {
	return BlockedEntry{
		Key: k.Name, Title: k.Title, Note: k.Status.Note, TS: k.Status.TS,
		By: k.Status.Author, ID: k.Status.ID, WaitingOn: b.waitingOnList(k, now),
	}
}

// heldEntry builds a held-list entry for k under the given kind ("claim"
// or "human"). kind "human" additionally carries the base status fields
// (Status, TS) that a claim entry never shows; whenever the key's current
// status is in-progress (always true for a claim entry, and true for a
// human entry that is ALSO actively claimed) it additionally carries the
// claim fields (Age, Stale — By/ID are already the base fields above,
// since status=in-progress's own latest event IS the claim). WaitingOn
// appears only when k has at least one unresolved edge — claimed-but-
// blocked and human-but-blocked are both legal and visible.
func (b *Board) heldEntry(k *Key, kind string, now time.Time) HeldEntry {
	e := HeldEntry{Key: k.Name, Title: k.Title, Kind: kind, By: k.Status.Author, ID: k.Status.ID}
	if kind == "human" {
		e.Status = k.Status.Value
		e.TS = k.Status.TS
	}
	if k.Status.Value == "in-progress" {
		stale, age := b.StaleAge(k, now)
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
