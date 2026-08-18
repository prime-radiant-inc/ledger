package cmd

import (
	"fmt"
	"io"
	"sort"

	"ledger/internal/fold"
	"ledger/internal/out"
	"ledger/internal/store"
)

type Ctx struct {
	Store  store.Store
	TTY    bool
	Stdout io.Writer
	Stderr io.Writer
	// StoreFlag is the raw --store value (possibly empty), always populated —
	// even for verbs like init that run before a store necessarily exists.
	StoreFlag string
	// Shadowed is the path of a store of the other kind higher in the
	// ancestry than the one ambient resolution chose (see store.Resolution).
	// Empty unless there is one — every place that would otherwise dead-end
	// in "nothing here" names it.
	Shadowed string
}

// Load reads and folds one ledger, ONCE: the same read that produces the
// event slice produces the sentinel-contracted DAG the fold order came
// from, so a verb needing the chain's shape (ready's contested pass) reads
// it off the ledger rather than folding the chain a second time.
func (c *Ctx) Load(slug string) (*fold.Ledger, error) {
	evs, meta, d, err := c.Store.EventsDAG(slug)
	if err != nil {
		return nil, out.Errf("unknown_ledger", c.shadowHint("ledger ls --all  (lists every ledger here)"),
			4, "no ledger '%s' here", slug)
	}
	led := fold.Fold(slug, evs, meta)
	led.DAG = d
	return led, nil
}

// shadowHint extends a "there's nothing here" hint with the other store in
// the ancestry, when there is one. Without it both dead ends — an unknown
// slug and an empty store — send the reader back into the same empty store
// they're already in, which is exactly how a whole investigation ledger in
// an ancestor's .ledger.git stayed invisible in the field.
func (c *Ctx) shadowHint(hint string) string {
	if c.Shadowed == "" {
		return hint
	}
	return hint + " — a second store exists at " + c.Shadowed + ": try --store " + c.Shadowed
}

// noteShadowedStore adds the same breadcrumb to a listing's payload and TTY
// lines: `ls` is where a reader goes to learn what's here, so it's where a
// store that isn't being read has to be named.
func (c *Ctx) noteShadowedStore(payload map[string]any, lines []string) []string {
	if c.Shadowed == "" {
		return lines
	}
	payload["shadowed_store"] = c.Shadowed
	return append(lines, "note: another ledger store exists at "+c.Shadowed+
		" (this one was chosen) — read the other with --store "+c.Shadowed)
}

func (c *Ctx) PickLedger(ledgerFlag string) (*fold.Ledger, error) {
	if ledgerFlag != "" {
		return c.Load(ledgerFlag)
	}
	slugs, err := c.Store.Slugs()
	if err != nil {
		return nil, out.Errf("git_failed", "", 1, "%s", err)
	}
	var all, opens []*fold.Ledger
	for _, s := range slugs {
		l, err := c.Load(s)
		if err != nil {
			continue
		}
		all = append(all, l)
		if l.State == "open" {
			opens = append(opens, l)
		}
	}
	switch len(opens) {
	case 1:
		return opens[0], nil
	case 0:
		// No open ledger, but if there's exactly one ledger overall (closed),
		// resolve to it anyway: the caller's own state check produces the
		// precise error (set: "closed" with successor hint; note: succeeds —
		// closed ledgers accept notes on the ambient path too).
		if len(all) == 1 {
			return all[0], nil
		}
		hint := "ledger create <slug> --scope <what-it-tracks>  starts one; ledger ls --all lists closed ones"
		if len(all) > 1 {
			hint += "; --ledger <slug> targets a closed one directly (notes and rollups are still allowed there)"
		}
		return nil, out.Errf("no_open_ledger", c.shadowHint(hint), 4, "no open ledgers in this repo")
	}
	sort.Slice(opens, func(i, j int) bool {
		return opens[i].Events[len(opens[i].Events)-1].TS > opens[j].Events[len(opens[j].Events)-1].TS
	})
	list := ""
	for i, l := range opens {
		if i > 0 {
			list += "; "
		}
		list += fmt.Sprintf("%s (%s, last write %s)", l.Slug, l.Meta.Scope, out.Age(l.Events[len(l.Events)-1].TS))
	}
	return nil, out.Errf("ambiguous_ledger", "add --ledger <slug>. Open: "+list, 4,
		"%d ledgers are open — say which one", len(opens))
}

func outEmit(c *Ctx, payload map[string]any, lines []string) {
	out.Emit(c.Stdout, c.TTY, payload, lines)
}
