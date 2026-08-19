// Package board derives per-key issue-tracker state (titles, labels,
// blocked-by edges, staleness) from a ledger's meta and event chain. Pure:
// a function of (meta, events) — no git access, no cmd dependency. Later
// tasks (ready, held/blocked/attention, show --where) build on this.
package board

import (
	"strings"
	"time"

	"ledger/internal/model"
)

// FieldState captures the latest write to a guarded field on a key: the
// value it landed, the event that landed it, and that event's provenance
// and message/evidence.
type FieldState struct {
	Value, ID, Author, TS, Note string
	Evidence                    []string
}

// RenameRecord is one landed rename event on a key: the title it asserted,
// plus the provenance a renamed title must always be able to show.
type RenameRecord struct{ Text, ID, Author, TS string }

// RenameInfo is the mandatory label a renamed title carries wherever a
// title-bearing row renders, JSON and TTY alike: who renamed it last, when,
// that rename event's id, and every title the key held before on the fold
// path — oldest first, the fold-path seed title included. It is fold-path
// history, not a complete inventory: under a two-root collision the losing
// root's seed title appears here nowhere (read both heads for that).
type RenameInfo struct {
	By    string   `json:"by"`
	TS    string   `json:"ts"`
	ID    string   `json:"id"`
	Prior []string `json:"prior"`
}

// Key is one issue-tracker key's derived state.
type Key struct {
	Name, Title string
	// SeedTitle is the first status event's message — the title law before
	// renames existed. Title equals it until a rename lands.
	SeedTitle string
	// Renames is every rename event on this key in fold order; the LAST
	// one's text is the key's current title. Concurrent renames resolve
	// last-in-fold-order, and the losers stay in the chain, greppable, and
	// listed among the prior titles.
	Renames     []RenameRecord
	Status      *FieldState // nil = statusless (no status event yet)
	LabelsID    string      // latest labels event id ("" if none)
	BlockedByID string
	// BlockedByTS is the latest blocked-by event's own timestamp — cycle
	// detection's break suggestion needs it to find the youngest edge in a
	// cycle (see frontier.go's cycleEntry); BlockedByID alone names the CAS
	// ticket but carries no ordering.
	BlockedByTS string
	// blockedBySeq is the latest blocked-by event's position in Build's
	// input events slice — the ledger's true chain order, exactly like
	// statusSeq below but for the blocked-by field. frontier.go's
	// youngerEdge falls back to this (never statusSeq) when BlockedByTS is
	// unparseable or tied: it's the position of the edge write itself, the
	// fact actually in question, not a proxy borrowed from a different
	// field. Unexported for the same reason statusSeq is: meaningful only
	// within a single Build() call.
	blockedBySeq int
	// Multi carries every declared multi-field's tokens by name, including
	// "labels" and "blocked-by" — the generic surface a ready-capable
	// board's third-or-later multi-field (e.g. "reviewers"; the shape
	// requires labels, guards blocked-by when declared, but caps nothing)
	// needs so show --where's membership clauses can filter on it too.
	// Labels() and BlockedBy() below read "labels" and "blocked-by" out of
	// here directly, so the fact is stored once, by construction, rather
	// than mirrored into dedicated fields. Latest write wins, wholesale
	// replace per set; nil when a key has no multi-field write at all.
	Multi map[string][]string
	// Fields is the generic complement to Status: the latest write per
	// declared enum field OTHER than "status" (e.g. a board-declared
	// "review" or "priority" field) — status keeps its dedicated field
	// alone and is never duplicated in here, since every other board
	// concept (guarded writes, titles, staleness, rule-5 signals) already
	// keys off Status directly. Multi-fields (labels, blocked-by) never
	// appear here either — they keep their own dedicated slices. Nil when
	// a key has no such write. Exists so show --where can filter on any
	// declared enum field, not just the three the board hard-codes.
	Fields map[string]*FieldState
	// statusSeq is the current Status event's position in Build's input
	// events slice — the ledger's true chain order. IDs are content-
	// addressed hashes with no inherent ordering, so this is the only way
	// to break a same-timestamp tie deterministically. Unexported: only
	// Envelope's ready-list sort (spec "chain-position ties") needs it,
	// and it means nothing outside a single Build() call.
	statusSeq int
}

// Board is the whole derived board: declarations plus every key's state.
type Board struct {
	Meta model.Meta
	Keys map[string]*Key
	// Contests holds the live write-head antichains per key (sync design,
	// Addition 3), keyed by key name and field-sorted within a key. Nil
	// until ComputeContests runs — Build alone cannot derive them, since a
	// contest is a property of the event DAG's shape, not of the fold.
	Contests map[string][]Contest
}

// Build folds events into per-key state in a single pass, in order.
// Latest event per (key, field) wins; multi-field values split on ",";
// an empty value clears. Title is a pure fold too: the LATEST rename
// event's text in fold order, else the first status event's message — so
// concurrent renames resolve by the same total order every other duel on
// this board resolves by, and the losers stay greppable in the chain.
func Build(meta model.Meta, events []model.Event) *Board {
	b := &Board{Meta: meta, Keys: map[string]*Key{}}
	for i, ev := range events {
		if ev.Type != "set" || ev.Key == "" {
			continue
		}
		k := b.Keys[ev.Key]
		if k == nil {
			k = &Key{Name: ev.Key}
			b.Keys[ev.Key] = k
		}
		if ev.Rename != "" {
			k.Renames = append(k.Renames, RenameRecord{Text: ev.Rename, ID: ev.ID, Author: ev.Author, TS: ev.TS})
		}
		for field, value := range ev.Fields {
			switch field {
			case "status":
				if k.Status == nil {
					k.SeedTitle = ev.Text // first status event's message
				}
				k.Status = &FieldState{
					Value: value, ID: ev.ID, Author: ev.Author, TS: ev.TS,
					Note: ev.Text, Evidence: ev.Evidence,
				}
				k.statusSeq = i
			case "labels":
				k.LabelsID = ev.ID
				k.setMulti(field, SplitTokens(value))
			case "blocked-by":
				k.BlockedByID = ev.ID
				k.BlockedByTS = ev.TS
				k.blockedBySeq = i
				k.setMulti(field, SplitTokens(value))
			default:
				if model.Contains(meta.MultiFields, field) {
					k.setMulti(field, SplitTokens(value))
					continue
				}
				if k.Fields == nil {
					k.Fields = map[string]*FieldState{}
				}
				k.Fields[field] = &FieldState{
					Value: value, ID: ev.ID, Author: ev.Author, TS: ev.TS,
					Note: ev.Text, Evidence: ev.Evidence,
				}
			}
		}
	}
	// The title resolves after the pass, never inside it: a rename can sit
	// BEFORE its key's seed in fold order (a two-root merge leaves exactly
	// that), so only the finished per-key rename list answers "which rename
	// is last". Fold-total by construction: a key with renames and no seed
	// still gets a title.
	for _, k := range b.Keys {
		k.Title = k.SeedTitle
		if n := len(k.Renames); n > 0 {
			k.Title = k.Renames[n-1].Text
		}
	}
	return b
}

// RenameInfo is the label a renamed key's title carries wherever it renders;
// nil when the key was never renamed — its title is still the seed's, and
// nothing needs saying.
func (k *Key) RenameInfo() *RenameInfo {
	n := len(k.Renames)
	if n == 0 {
		return nil
	}
	prior := []string{}
	if k.SeedTitle != "" {
		prior = append(prior, k.SeedTitle)
	}
	for _, r := range k.Renames[:n-1] {
		prior = append(prior, r.Text)
	}
	last := k.Renames[n-1]
	return &RenameInfo{By: last.Author, TS: last.TS, ID: last.ID, Prior: prior}
}

// setMulti records field's latest tokens in k.Multi — wholesale replace,
// matching Labels'/BlockedBy's own clear-on-empty semantics.
func (k *Key) setMulti(field string, tokens []string) {
	if k.Multi == nil {
		k.Multi = map[string][]string{}
	}
	k.Multi[field] = tokens
}

// Labels is k's latest "labels" multi-field tokens, empty ok.
func (k *Key) Labels() []string { return k.Multi["labels"] }

// BlockedBy is k's latest "blocked-by" multi-field tokens, empty ok.
func (k *Key) BlockedBy() []string { return k.Multi["blocked-by"] }

// SplitTokens parses a comma-joined multi-field value; an empty value
// (the clear case) yields no tokens.
func SplitTokens(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// IsTerminal reports whether value is a member of the board's declared
// terminal status vocab (Meta.Terminal["status"]).
func (b *Board) IsTerminal(value string) bool {
	for _, v := range b.Meta.Terminal["status"] {
		if v == value {
			return true
		}
	}
	return false
}

// HasHuman reports whether the key carries the reserved "human" label
// token, among however many other tokens it has.
func (k *Key) HasHuman() bool {
	for _, l := range k.Labels() {
		if l == "human" {
			return true
		}
	}
	return false
}

// StaleAge reports the key's claim age (now − the status event's own TS)
// and whether that age makes it stale. Staleness requires status ==
// "in-progress", a declared Meta.StaleAfter, and age exceeding it —
// computed from the claim event's own timestamp, never the wall clock.
// Age is still reported when the key isn't currently a stale-eligible
// claim (e.g. a live, non-stale claim) so callers can render it either
// way; a statusless key or an unparseable timestamp reports zero age.
func (b *Board) StaleAge(k *Key, now time.Time) (stale bool, age time.Duration) {
	if k.Status == nil {
		return false, 0
	}
	claimTS, err := model.ParseTS(k.Status.TS)
	if err != nil {
		return false, 0
	}
	age = now.Sub(claimTS)
	if age < 0 {
		// General age clamp (sync spec Addition 4/1(c)): a claim event
		// newer than the evaluation clock — a peer host whose clock runs
		// ahead, or --at fixed in the past — renders age 0s rather than a
		// negative duration. The clamp is silent on the reclaim side: it
		// keeps the number honest but the replica cannot see this claim go
		// stale until its own clock passes the claim's timestamp.
		age = 0
	}
	if k.Status.Value != "in-progress" || b.Meta.StaleAfter == "" {
		return false, age
	}
	horizon, err := time.ParseDuration(b.Meta.StaleAfter)
	if err != nil {
		return false, age
	}
	return age > horizon, age
}

// FormatAge renders a duration the way the envelope shows ages:
// time.Duration.String(), truncated to whole seconds — except a sub-second
// age, which truncating to whole seconds would flatten to the misleading
// "0s" (a stale claim seconds old rendering identically to a brand new
// one); truncate to whole milliseconds instead so it reads as e.g. "600ms".
func FormatAge(d time.Duration) string {
	if d < time.Second {
		return d.Truncate(time.Millisecond).String()
	}
	return d.Truncate(time.Second).String()
}

// Signal is one standing signal guarding a write on a key (spec rule 5):
// Name is one of "claim", "human", "settled"; Facts is its pinned
// human-readable detail (claimant and age; the label; the settled value
// and its evidence state).
type Signal struct{ Name, Facts string }

// Signals reports the standing signals guarding a write on key k, in the
// spec's fixed order: claim, human, settled. Rule 5 exists only on
// ready-capable boards, and only for a guarded write — callers must not
// invoke this for an unguarded (e.g. labels-only) write, since human
// otherwise "gates every guarded write", not every write. touchesStatus
// scopes claim and settled to status writes only; human is key-scoped and
// always checked once a caller decides the write is guarded. A nil k (a
// key no event has ever touched) carries no signals — there is nothing to
// check against.
func (b *Board) Signals(k *Key, touchesStatus bool, author string, now time.Time) []Signal {
	if !model.ReadyCapable(b.Meta) || k == nil {
		return nil
	}
	var signals []Signal
	if touchesStatus && k.Status != nil && k.Status.Value == "in-progress" && k.Status.Author != author {
		if stale, age := b.StaleAge(k, now); !stale {
			signals = append(signals, Signal{Name: "claim", Facts: k.Status.Author + ", " + FormatAge(age)})
		}
	}
	if k.HasHuman() {
		signals = append(signals, Signal{Name: "human", Facts: "labeled 'human'"})
	}
	if touchesStatus && k.Status != nil && b.IsTerminal(k.Status.Value) {
		evidence := "no"
		if len(k.Status.Evidence) > 0 {
			evidence = "yes"
		}
		signals = append(signals, Signal{Name: "settled", Facts: k.Status.Value + ", evidence: " + evidence})
	}
	return signals
}
