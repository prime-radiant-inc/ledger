// Package fold derives current ledger state from the event chain.
// Everything mutable folds from events; meta.json holds only creation facts.
package fold

import (
	"sort"

	"ledger/internal/model"
)

type Ledger struct {
	Slug    string
	Meta    model.Meta
	Schema  map[string][]string
	Require map[string][]string
	Spine   map[string]map[string]model.Event
	// MultiFields and Terminal are copied straight from meta (spike addition):
	// declared multi-valued/vocab-free field names, and per-field terminal
	// values for the `ready` verb's blocked-by resolution.
	MultiFields  []string
	Terminal     map[string][]string
	State        string
	SupersededBy string
	ExtraLinks   []string
	Events       []model.Event
	// Parent maps a child event id to the rollup id that encapsulates it —
	// winners only. Losers holds rollup ids that lost a duel: a rollup with
	// ANY already-taken child loses wholly (its summary line is a claim
	// about its entire child set), stays in the raw chain, and is inert.
	Parent map[string]string
	Losers map[string]bool
}

func Fold(slug string, evs []model.Event, meta model.Meta) *Ledger {
	l := &Ledger{Slug: slug, Meta: meta, State: "open",
		Schema: map[string][]string{}, Require: map[string][]string{},
		MultiFields: append([]string{}, meta.MultiFields...), Terminal: map[string][]string{},
		Spine: map[string]map[string]model.Event{}, Events: evs,
		Parent: map[string]string{}, Losers: map[string]bool{}}
	for f, v := range meta.Fields {
		if v == nil {
			l.Schema[f] = nil
		} else {
			l.Schema[f] = append([]string{}, v...)
		}
	}
	for f, v := range meta.RequireEvidence {
		l.Require[f] = append([]string{}, v...)
	}
	for f, v := range meta.Terminal {
		l.Terminal[f] = append([]string{}, v...)
	}
	for _, ev := range evs {
		switch ev.Type {
		case "sync":
			// sync events are skipped entirely (invisible to schema/spine/state)
			continue
		case "vocab":
			if cur, ok := l.Schema[ev.Field]; ok && cur != nil && !contains(cur, ev.Value) {
				l.Schema[ev.Field] = append(cur, ev.Value)
			}
		case "set":
			for f := range ev.Fields {
				if l.Spine[ev.Key] == nil {
					l.Spine[ev.Key] = map[string]model.Event{}
				}
				l.Spine[ev.Key][f] = ev
			}
		case "lifecycle":
			switch ev.LifecycleKind {
			case "close":
				if l.State == "open" { // first close in total order wins (spec: dueling closes)
					l.State = "closed:" + ev.Reason
				}
			case "superseded_by":
				if l.SupersededBy == "" {
					l.SupersededBy = ev.Successor // first link wins the redirect
				} else {
					l.ExtraLinks = append(l.ExtraLinks, ev.Successor)
				}
			}
		case "rollup":
			taken := false
			for _, c := range ev.Children {
				if _, ok := l.Parent[c]; ok {
					taken = true
					break
				}
			}
			if taken {
				l.Losers[ev.ID] = true
			} else {
				for _, c := range ev.Children {
					l.Parent[c] = ev.ID
				}
			}
		}
	}
	return l
}

// IsMultiField reports whether name was declared with `create --multi-field`.
func (l *Ledger) IsMultiField(name string) bool {
	return contains(l.MultiFields, name)
}

func (l *Ledger) Head() string {
	if len(l.Events) == 0 {
		return ""
	}
	return l.Events[len(l.Events)-1].ID
}

func (l *Ledger) Notes() []model.Event {
	var out []model.Event
	for _, e := range l.Events {
		if e.Type == "note" {
			out = append(out, e)
		}
	}
	return out
}

// Roots returns every record not encapsulated by anything — the curated
// history — in causal order: each root sorts by the chain position of its
// earliest transitive base event, not the rollup commit's own position,
// which would sort curated threads after the live work they causally
// precede (spec rev 12; the rollup eval's one real defect).
func (l *Ledger) Roots() []model.Event {
	pos := map[string]int{}
	byID := map[string]model.Event{}
	for i, e := range l.Events {
		pos[e.ID] = i
		byID[e.ID] = e
	}
	memo := map[string]int{}
	var earliest func(id string) int
	earliest = func(id string) int {
		if v, ok := memo[id]; ok {
			return v
		}
		e, ok := byID[id]
		if !ok {
			return int(^uint(0) >> 1) // unknown child id: sort last, never crash
		}
		min := pos[id]
		if e.Type == "rollup" && len(e.Children) > 0 && !l.Losers[id] {
			min = int(^uint(0) >> 1)
			for _, c := range e.Children {
				if p := earliest(c); p < min {
					min = p
				}
			}
		}
		memo[id] = min
		return min
	}
	var roots []model.Event
	for _, e := range l.Events {
		if e.Type == "sync" || l.Losers[e.ID] {
			continue
		}
		if _, taken := l.Parent[e.ID]; taken {
			continue
		}
		roots = append(roots, e)
	}
	sort.SliceStable(roots, func(i, j int) bool {
		return earliest(roots[i].ID) < earliest(roots[j].ID)
	})
	return roots
}

// Due is the advisory curation debt: unencapsulated non-rollup events.
// A root rollup is finished work, not debt (spec rev 12).
func (l *Ledger) Due() int {
	n := 0
	for _, e := range l.Roots() {
		if e.Type != "rollup" {
			n++
		}
	}
	return n
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
