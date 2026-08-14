// Package fold derives current ledger state from the event chain.
// Everything mutable folds from events; meta.json holds only creation facts.
package fold

import "ledger/internal/model"

type Ledger struct {
	Slug         string
	Meta         model.Meta
	Schema       map[string][]string
	Require      map[string][]string
	Spine        map[string]map[string]model.Event
	State        string
	SupersededBy string
	ExtraLinks   []string
	Events       []model.Event
}

func Fold(slug string, evs []model.Event, meta model.Meta) *Ledger {
	l := &Ledger{Slug: slug, Meta: meta, State: "open",
		Schema: map[string][]string{}, Require: map[string][]string{},
		Spine: map[string]map[string]model.Event{}, Events: evs}
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
		}
	}
	return l
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

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
