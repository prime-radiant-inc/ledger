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

// Key is one issue-tracker key's derived state.
type Key struct {
	Name, Title string
	Status      *FieldState // nil = statusless (no status event yet)
	Labels      []string    // parsed tokens, empty ok
	LabelsID    string      // latest labels event id ("" if none)
	BlockedBy   []string
	BlockedByID string
}

// Board is the whole derived board: declarations plus every key's state.
type Board struct {
	Meta model.Meta
	Keys map[string]*Key
}

// Build folds events into per-key state in a single pass, in order.
// Latest event per (key, field) wins; multi-field values split on ",";
// an empty value clears. Title is fixed to the Text of the key's first
// status-setting event and never revisited by later status writes.
func Build(meta model.Meta, events []model.Event) *Board {
	b := &Board{Meta: meta, Keys: map[string]*Key{}}
	for _, ev := range events {
		if ev.Type != "set" || ev.Key == "" {
			continue
		}
		k := b.Keys[ev.Key]
		if k == nil {
			k = &Key{Name: ev.Key}
			b.Keys[ev.Key] = k
		}
		for field, value := range ev.Fields {
			switch field {
			case "status":
				if k.Status == nil {
					k.Title = ev.Text // first status event's message, immutable
				}
				k.Status = &FieldState{
					Value: value, ID: ev.ID, Author: ev.Author, TS: ev.TS,
					Note: ev.Text, Evidence: ev.Evidence,
				}
			case "labels":
				k.Labels = splitTokens(value)
				k.LabelsID = ev.ID
			case "blocked-by":
				k.BlockedBy = splitTokens(value)
				k.BlockedByID = ev.ID
			}
		}
	}
	return b
}

// splitTokens parses a comma-joined multi-field value; an empty value
// (the clear case) yields no tokens.
func splitTokens(v string) []string {
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
	for _, l := range k.Labels {
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
// time.Duration.String(), truncated to whole seconds.
func FormatAge(d time.Duration) string {
	return d.Truncate(time.Second).String()
}
