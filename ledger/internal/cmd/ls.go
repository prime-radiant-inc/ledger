package cmd

import (
	"github.com/spf13/cobra"
)

func init() { register(newLsCmd) }

func newLsCmd(c *Ctx) *cobra.Command {
	var all bool
	cmd := &cobra.Command{Use: "ls", Short: "list ledgers with freshness", RunE: func(_ *cobra.Command, _ []string) error {
		return runLs(c, all)
	}}
	cmd.Flags().BoolVar(&all, "all", false, "include closed ledgers")
	return cmd
}

func runLs(c *Ctx, all bool) error {
	slugs, err := c.Store.Slugs()
	if err != nil {
		return err
	}
	// Minimal slug+state listing so other verbs (create --supersedes, close)
	// have something to check against; recency sort/idle marking/30d window
	// arrive in the dedicated ls task.
	rows := []map[string]any{}
	lines := []string{}
	for _, s := range slugs {
		led, err := c.Load(s)
		if err != nil {
			continue
		}
		if !all && led.State != "open" {
			continue
		}
		rows = append(rows, map[string]any{"slug": s, "state": led.State, "scope": led.Meta.Scope})
		lines = append(lines, s+" ("+led.State+")")
	}
	if len(rows) == 0 {
		outEmit(c, map[string]any{"ledgers": rows}, []string{"no ledgers in this repo — ledger create <slug> --scope <ref> starts one"})
		return nil
	}
	outEmit(c, map[string]any{"ledgers": rows}, lines)
	return nil
}
