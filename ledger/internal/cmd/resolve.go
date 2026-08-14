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
}

func (c *Ctx) Load(slug string) (*fold.Ledger, error) {
	evs, meta, err := c.Store.Events(slug)
	if err != nil {
		return nil, out.Errf("unknown_ledger", "ledger ls --all  (lists every ledger here)", 4, "no ledger '%s' here", slug)
	}
	return fold.Fold(slug, evs, meta), nil
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
			hint += "; --ledger <slug> targets a closed one directly (notes are still allowed there)"
		}
		return nil, out.Errf("no_open_ledger", hint, 4, "no open ledgers in this repo")
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
