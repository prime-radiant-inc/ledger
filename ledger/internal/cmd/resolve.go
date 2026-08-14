package cmd

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"ledger/internal/fold"
	"ledger/internal/out"
	"ledger/internal/store"
)

type Ctx struct {
	Store  store.Store
	TTY    bool
	Stdout io.Writer
	Stderr io.Writer
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
	var opens []*fold.Ledger
	for _, s := range slugs {
		l, err := c.Load(s)
		if err == nil && l.State == "open" {
			opens = append(opens, l)
		}
	}
	switch len(opens) {
	case 1:
		return opens[0], nil
	case 0:
		return nil, out.Errf("no_open_ledger",
			"ledger create <slug> --scope <what-it-tracks>  starts one; ledger ls --all lists closed ones",
			4, "no open ledgers in this repo")
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

// in resolve.go for now; moved to read.go in Task 9
func init() { register(newStatusStub) }
func newStatusStub(c *Ctx) *cobra.Command {
	var ledgerFlag string
	cmd := &cobra.Command{Use: "status [key]", Short: "the spine: latest value per item", RunE: func(_ *cobra.Command, _ []string) error {
		_, err := c.PickLedger(ledgerFlag)
		return err
	}}
	cmd.Flags().StringVar(&ledgerFlag, "ledger", "", "target ledger")
	return cmd
}
